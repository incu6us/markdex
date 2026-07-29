package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// blockingIngester blocks each Ingest until released (or its ctx dies), so
// tests can hold the worker busy and fill the queue deterministically.
type blockingIngester struct {
	started  chan string
	release  chan struct{}
	canceled chan struct{}
}

func newBlockingIngester() *blockingIngester {
	return &blockingIngester{
		started:  make(chan string, 128),
		release:  make(chan struct{}),
		canceled: make(chan struct{}, 128),
	}
}

func (b *blockingIngester) Ingest(ctx context.Context, spec IngestSpec, _ func(int, int)) (int, error) {
	b.started <- spec.Collection
	select {
	case <-b.release:
		return 1, nil
	case <-ctx.Done():
		b.canceled <- struct{}{}
		return 0, ctx.Err()
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSubmitReturnsErrQueueFullInsteadOfBlocking(t *testing.T) {
	t.Parallel()
	ing := newBlockingIngester()
	m := NewJobManager(ing, discardLogger())
	m.Start()
	defer m.Stop()

	// 1 in-flight + 64 buffered fit; nothing beyond that may block the caller.
	fill(t, m, ing, 65)

	done := make(chan error, 1)
	go func() {
		_, err := m.Submit(IngestSpec{Collection: "overflow"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("err = %v, want ErrQueueFull", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked on a full queue")
	}
}

func TestStopCancelsInFlightAndFailsQueued(t *testing.T) {
	t.Parallel()
	ing := newBlockingIngester()
	m := NewJobManager(ing, discardLogger())
	m.Start()

	ids := fill(t, m, ing, 3) // 1 running + 2 queued

	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()

	select {
	case <-ing.canceled: // in-flight job saw ctx cancellation
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight ingest was not canceled by Stop")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return (drained the queue instead of canceling?)")
	}

	for _, id := range ids {
		job, ok := m.Get(id)
		if !ok {
			t.Fatalf("job %s vanished", id)
		}
		if job.State != JobFailed {
			t.Fatalf("job %s state = %q, want failed after shutdown", id, job.State)
		}
	}
}

func TestSubmitAfterStopFailsCleanly(t *testing.T) {
	t.Parallel()
	m := NewJobManager(newBlockingIngester(), discardLogger())
	m.Start()
	m.Stop()

	if _, err := m.Submit(IngestSpec{Collection: "late"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull after Stop", err)
	}
}

func TestTerminalJobsEvictedAfterTTL(t *testing.T) {
	t.Parallel()
	m := NewJobManager(newBlockingIngester(), discardLogger())

	m.mu.Lock()
	m.jobs["old"] = &Job{ID: "old", State: JobSucceeded, doneAt: time.Now().Add(-2 * jobTTL)}
	m.jobs["fresh"] = &Job{ID: "fresh", State: JobSucceeded, doneAt: time.Now()}
	m.jobs["running"] = &Job{ID: "running", State: JobRunning}
	m.sweepLocked(time.Now())
	m.mu.Unlock()

	if _, ok := m.Get("old"); ok {
		t.Fatal("expired terminal job was not evicted")
	}
	for _, id := range []string{"fresh", "running"} {
		if _, ok := m.Get(id); !ok {
			t.Fatalf("job %s must survive the sweep", id)
		}
	}
}

// fill submits n jobs, synchronizing on the first actually reaching the
// worker so the queue buffer is free for the remaining n-1.
func fill(t *testing.T, m *JobManager, ing *blockingIngester, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := m.Submit(IngestSpec{Collection: "c"})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		ids = append(ids, id)
		if i == 0 {
			select {
			case <-ing.started: // worker picked it up; buffer slot freed
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not pick up the first job")
			}
		}
	}
	return ids
}

func TestWorkerCountBoundsConcurrentIngests(t *testing.T) {
	t.Parallel()
	ing := newBlockingIngester()
	m := NewJobManager(ing, discardLogger(), WithWorkers(3))
	m.Start()
	defer m.Stop()

	for i := range 4 {
		if _, err := m.Submit(IngestSpec{Collection: string(rune('a' + i))}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	// Exactly the configured number of workers may be embedding at once: the
	// embedder is CPU-bound and shared with live voice traffic, so this bound is
	// what keeps an upload from starving a call.
	for i := range 3 {
		select {
		case <-ing.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 3 workers picked up a job", i)
		}
	}
	select {
	case c := <-ing.started:
		t.Fatalf("a 4th job (%q) started while 3 workers were busy", c)
	case <-time.After(200 * time.Millisecond):
	}

	close(ing.release)
}

func TestQueueDepthIsConfigurable(t *testing.T) {
	t.Parallel()
	ing := newBlockingIngester()
	m := NewJobManager(ing, discardLogger(), WithQueueDepth(2))
	m.Start()
	defer m.Stop()

	// 1 in-flight + 2 buffered fit; the next submission is refused rather than
	// queued behind an unbounded backlog.
	fill(t, m, ing, 3)
	if _, err := m.Submit(IngestSpec{Collection: "overflow"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull once the configured depth is used up", err)
	}
}

func TestJobManagerDefaultsStaySingleWorkerAndDeepQueue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		opts    []JobManagerOption
		workers int
		depth   int
	}{
		{name: "no options", workers: 1, depth: 64},
		{name: "zero is ignored", opts: []JobManagerOption{WithWorkers(0), WithQueueDepth(0)}, workers: 1, depth: 64},
		{name: "negative is ignored", opts: []JobManagerOption{WithWorkers(-4), WithQueueDepth(-1)}, workers: 1, depth: 64},
	} {
		m := NewJobManager(newBlockingIngester(), discardLogger(), tc.opts...)
		if got := m.workers; got != tc.workers {
			t.Errorf("%s: workers = %d, want %d", tc.name, got, tc.workers)
			continue
		}
		if got := cap(m.queue); got != tc.depth {
			t.Errorf("%s: queue depth = %d, want %d", tc.name, got, tc.depth)
			continue
		}
	}
}

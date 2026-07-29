package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobState string

const (
	JobPending   JobState = "pending"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
)

type Job struct {
	ID         string   `json:"id"`
	Collection string   `json:"collection"`
	State      JobState `json:"state"`
	Processed  int      `json:"processed"`
	Total      int      `json:"total"`
	Ingested   int      `json:"ingested"`
	Error      string   `json:"error,omitempty"`

	doneAt time.Time // when the job reached a terminal state; drives TTL eviction
}

// FileSpec is one in-memory file (name + content), used for folder upload.
type FileSpec struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// maxUploadFiles caps how many files a single folder-upload job may carry.
const maxUploadFiles = 2000

type IngestSpec struct {
	Name       string
	Content    string
	Collection string
	MaxChars   int
	Overlap    int
	// URLs, when set, are raw .md URLs to fetch and ingest as one job (repo ingestion).
	URLs []string
	// Files, when set, are in-memory documents to ingest as one job (folder upload).
	Files []FileSpec
	// ReconcileScope, when set, prunes chunks whose source_id has this prefix but was
	// not in this ingest (deleted-from-repo cleanup); empty disables reconciliation.
	ReconcileScope string
}

// rawScope returns the scheme://host/owner/repo/ prefix of a raw GitHub file URL.
// It is a sanity floor for listingScope, never the reconcile scope itself.
func rawScope(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/" + parts[0] + "/" + parts[1] + "/"
}

// listingScope returns the longest common directory prefix of the listed raw
// URLs — the reconcile scope. Deriving it from the listing itself means it
// always carries the ref and subpath, so pruning can never reach files outside
// what was just listed (re-ingesting a subpath or another branch must not
// delete chunks for files that still exist in the repo).
//
// When every listed file happens to share a directory deeper than the
// requested subpath, the scope is narrower than the request and orphans
// outside it survive until a broader re-ingest — under-pruning is the safe
// direction, data loss is not.
func listingScope(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	prefix := urls[0]
	for _, u := range urls[1:] {
		for !strings.HasPrefix(u, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	i := strings.LastIndexByte(prefix, '/')
	if i < 0 {
		return ""
	}
	scope := prefix[:i+1]
	// Sanity floor: the scope must stay within a single repo prefix.
	if repo := rawScope(urls[0]); repo == "" || !strings.HasPrefix(scope, repo) {
		return ""
	}
	return scope
}

type Ingester interface {
	Ingest(ctx context.Context, spec IngestSpec, report func(processed, total int)) (int, error)
}

type task struct {
	id   string
	spec IngestSpec
}

// ErrQueueFull is returned by Submit when the ingest queue has no free slot;
// callers should surface it as 503 and let the client retry.
var ErrQueueFull = errors.New("ingestion queue is full")

// jobTTL is how long a terminal (succeeded/failed) job stays queryable before
// being evicted; without it the jobs map grows for the life of the process.
const jobTTL = time.Hour

// Defaults: one worker, because ingestion is bound by the CPU embedder, which
// on a shared host competes with latency-sensitive traffic; and a queue deep
// enough that a burst waits instead of being refused.
const (
	defaultWorkers    = 1
	defaultQueueDepth = 64
)

type JobManager struct {
	ingester Ingester
	logger   *slog.Logger
	queue    chan task
	workers  int
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	mu   sync.Mutex
	jobs map[string]*Job
}

// JobManagerOption tunes the ingestion pool. Non-positive values are ignored so
// an unset or malformed environment variable keeps the safe default.
type JobManagerOption func(*JobManager)

// WithWorkers sets how many jobs may be ingested concurrently.
func WithWorkers(n int) JobManagerOption {
	return func(m *JobManager) {
		if n > 0 {
			m.workers = n
		}
	}
}

// WithQueueDepth sets how many jobs may wait before Submit reports ErrQueueFull.
func WithQueueDepth(n int) JobManagerOption {
	return func(m *JobManager) {
		if n > 0 {
			m.queue = make(chan task, n)
		}
	}
}

func NewJobManager(ingester Ingester, logger *slog.Logger, opts ...JobManagerOption) *JobManager {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &JobManager{
		ingester: ingester,
		logger:   logger,
		queue:    make(chan task, defaultQueueDepth),
		workers:  defaultWorkers,
		jobs:     make(map[string]*Job),
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *JobManager) Start() {
	for range m.workers {
		m.wg.Go(func() {
			for {
				select {
				case <-m.ctx.Done():
					m.failQueued()
					return
				case t := <-m.queue:
					m.run(t)
				}
			}
		})
	}
}

// Stop cancels the in-flight job (its context aborts the next network call)
// and fails still-queued jobs instead of running the whole backlog to
// completion — shutdown must not take hours. The queue is never closed, so a
// racing Submit degrades to ErrQueueFull rather than a send-on-closed panic.
func (m *JobManager) Stop() {
	m.cancel()
	m.wg.Wait()
}

// Done is closed when the manager shuts down; long-lived watchers (SSE job
// streams) select on it so open connections don't stall graceful shutdown.
func (m *JobManager) Done() <-chan struct{} {
	return m.ctx.Done()
}

func (m *JobManager) failQueued() {
	for {
		select {
		case t := <-m.queue:
			m.update(t.id, func(job *Job) {
				job.State = JobFailed
				job.Error = "canceled: server shutting down"
			})
		default:
			return
		}
	}
}

func (m *JobManager) Submit(spec IngestSpec) (string, error) {
	if m.ctx.Err() != nil {
		return "", ErrQueueFull
	}
	id := uuid.NewString()
	m.mu.Lock()
	m.sweepLocked(time.Now())
	m.jobs[id] = &Job{ID: id, Collection: spec.Collection, State: JobPending}
	m.mu.Unlock()

	select {
	case m.queue <- task{id: id, spec: spec}:
		return id, nil
	default:
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		return "", ErrQueueFull
	}
}

// sweepLocked evicts terminal jobs older than jobTTL. Called with mu held.
func (m *JobManager) sweepLocked(now time.Time) {
	for id, job := range m.jobs {
		if !job.doneAt.IsZero() && now.Sub(job.doneAt) > jobTTL {
			delete(m.jobs, id)
		}
	}
}

func (m *JobManager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *job, true
}

func (m *JobManager) run(t task) {
	m.update(t.id, func(job *Job) { job.State = JobRunning })

	report := func(processed, total int) {
		m.update(t.id, func(job *Job) {
			job.Processed = processed
			job.Total = total
		})
	}

	ingested, err := m.ingester.Ingest(m.ctx, t.spec, report)
	m.update(t.id, func(job *Job) {
		if err != nil {
			job.State = JobFailed
			job.Error = err.Error()
			return
		}
		job.State = JobSucceeded
		job.Ingested = ingested
	})
	if err != nil {
		m.logger.Error("ingest job failed", "id", t.id, "collection", t.spec.Collection, "err", err)
	}
}

func (m *JobManager) update(id string, mutate func(*Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, ok := m.jobs[id]; ok {
		mutate(job)
		if (job.State == JobSucceeded || job.State == JobFailed) && job.doneAt.IsZero() {
			job.doneAt = time.Now()
		}
	}
}

type ingestRequest struct {
	Source     sourceRequest `json:"source"`
	Collection string        `json:"collection"`
	MaxChars   int           `json:"max_chars"`
	Overlap    int           `json:"overlap"`
	// Prune (repo sources only): after ingest, delete chunks for files that no longer
	// exist in the repo — collection reconciliation, scoped to that repo.
	Prune bool `json:"prune"`
}

type ingestResponse struct {
	JobID string `json:"job_id"`
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req ingestRequest
	if err := decodeJSON(w, r, &req, maxIngestBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(req.Collection) == "" {
		writeError(w, http.StatusBadRequest, "collection is required")
		return
	}
	if !checkCollectionName(w, req.Collection) {
		return
	}

	spec := IngestSpec{Collection: req.Collection, MaxChars: req.MaxChars, Overlap: req.Overlap}
	switch req.Source.Type {
	case "github_repo":
		if s.repoLister == nil {
			writeError(w, http.StatusBadRequest, "github repo source is not configured")
			return
		}
		urls, err := s.repoLister.ListMarkdown(r.Context(), req.Source.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec.URLs = urls
		if req.Prune && len(urls) > 0 {
			spec.ReconcileScope = listingScope(urls)
			if spec.ReconcileScope == "" {
				// Never silently skip a reconciliation the caller asked for.
				s.logger.Warn("prune requested but no reconcile scope could be derived; skipping prune",
					"collection", req.Collection, "url", req.Source.URL)
			}
		}
	case "upload_dir":
		if len(req.Source.Files) == 0 {
			writeError(w, http.StatusBadRequest, "no files provided")
			return
		}
		if len(req.Source.Files) > maxUploadFiles {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("too many files (max %d)", maxUploadFiles))
			return
		}
		spec.Files = req.Source.Files
	default:
		doc, err := s.resolveSource(r.Context(), req.Source)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec.Name, spec.Content = doc.Path(), doc.Content()
	}

	if err := s.validateTarget(r.Context(), req.Collection); err != nil {
		if errors.Is(err, errMismatch) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "failed to validate collection")
		return
	}

	jobID, err := s.jobs.Submit(spec)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ingestion queue is full; retry later")
		return
	}
	writeJSON(w, http.StatusAccepted, ingestResponse{JobID: jobID})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.jobs.Get(id); !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		job, ok := s.jobs.Get(id)
		if !ok {
			return
		}
		data, err := json.Marshal(job)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		if job.State == JobSucceeded || job.State == JobFailed {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.jobs.Done():
			// Manager is shutting down — end the stream so open connections
			// don't hold the HTTP server past its shutdown deadline.
			return
		case <-ticker.C:
		}
	}
}

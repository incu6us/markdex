package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
}

type IngestSpec struct {
	Name       string
	Content    string
	Collection string
	MaxChars   int
	Overlap    int
}

type Ingester interface {
	Ingest(ctx context.Context, spec IngestSpec, report func(processed, total int)) (int, error)
}

type task struct {
	id   string
	spec IngestSpec
}

type JobManager struct {
	ingester Ingester
	logger   *slog.Logger
	queue    chan task
	wg       sync.WaitGroup

	mu   sync.Mutex
	jobs map[string]*Job
}

func NewJobManager(ingester Ingester, logger *slog.Logger) *JobManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &JobManager{
		ingester: ingester,
		logger:   logger,
		queue:    make(chan task, 64),
		jobs:     make(map[string]*Job),
	}
}

func (m *JobManager) Start() {
	m.wg.Go(func() {
		for t := range m.queue {
			m.run(t)
		}
	})
}

func (m *JobManager) Stop() {
	close(m.queue)
	m.wg.Wait()
}

func (m *JobManager) Submit(spec IngestSpec) string {
	id := uuid.NewString()
	m.mu.Lock()
	m.jobs[id] = &Job{ID: id, Collection: spec.Collection, State: JobPending}
	m.mu.Unlock()

	m.queue <- task{id: id, spec: spec}
	return id
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

	ingested, err := m.ingester.Ingest(context.Background(), t.spec, report)
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
	}
}

type ingestRequest struct {
	Source     sourceRequest `json:"source"`
	Collection string        `json:"collection"`
	MaxChars   int           `json:"max_chars"`
	Overlap    int           `json:"overlap"`
}

type ingestResponse struct {
	JobID string `json:"job_id"`
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req ingestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Collection) == "" {
		writeError(w, http.StatusBadRequest, "collection is required")
		return
	}

	doc, err := s.resolveSource(r.Context(), req.Source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateTarget(r.Context(), req.Collection); err != nil {
		if errors.Is(err, errMismatch) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "failed to validate collection")
		return
	}

	id := s.jobs.Submit(IngestSpec{
		Name:       doc.Path(),
		Content:    doc.Content(),
		Collection: req.Collection,
		MaxChars:   req.MaxChars,
		Overlap:    req.Overlap,
	})
	writeJSON(w, http.StatusAccepted, ingestResponse{JobID: id})
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
		case <-ticker.C:
		}
	}
}

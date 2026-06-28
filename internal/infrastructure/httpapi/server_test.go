package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incu6us/markdex/internal/infrastructure/httpapi"
	"github.com/incu6us/markdex/internal/infrastructure/markdown"
)

type stubLister struct {
	collections []httpapi.Collection
	err         error
}

func (s stubLister) List(context.Context) ([]httpapi.Collection, error) {
	return s.collections, s.err
}

type stubCreator struct {
	mu      sync.Mutex
	created []string
	err     error
}

func (s *stubCreator) Create(_ context.Context, name string) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	s.created = append(s.created, name)
	s.mu.Unlock()
	return nil
}

type stubFetcher struct {
	content string
	err     error
}

func (s stubFetcher) Fetch(context.Context, string) (string, error) {
	return s.content, s.err
}

type stubIngester struct {
	ingested int
	err      error
}

func (s stubIngester) Ingest(_ context.Context, _ httpapi.IngestSpec, report func(int, int)) (int, error) {
	report(s.ingested, s.ingested)
	return s.ingested, s.err
}

type testDeps struct {
	lister   httpapi.CollectionLister
	creator  httpapi.CollectionCreator
	fetcher  httpapi.Fetcher
	ingester httpapi.Ingester
	model    httpapi.ModelInfo
}

func newTestServer(t *testing.T, deps testDeps) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if deps.ingester == nil {
		deps.ingester = stubIngester{}
	}
	if deps.lister == nil {
		deps.lister = stubLister{}
	}
	manager := httpapi.NewJobManager(deps.ingester, logger)
	manager.Start()
	t.Cleanup(manager.Stop)

	return httpapi.NewServer(httpapi.Config{
		Chunker: markdown.NewSplitter(2000, 200),
		Fetcher: deps.fetcher,
		Lister:  deps.lister,
		Creator: deps.creator,
		Jobs:    manager,
		Model:   deps.model,
		Logger:  logger,
	}).Handler()
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func uploadSource(name, content string) map[string]any {
	return map[string]any{"source": map[string]any{"type": "upload", "name": name, "content": content}}
}

func TestHandlePreviewUpload(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{})
	rec := doRequest(t, handler, http.MethodPost, "/api/preview",
		uploadSource("go.md", "intro\n\n# Naming\nkeep names short\n\n# Errors\nwrap with percent w"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	resp := decodeBody[struct {
		Name        string `json:"name"`
		TotalChunks int    `json:"total_chunks"`
		Topics      []struct {
			Title       string `json:"title"`
			HeadingPath string `json:"heading_path"`
		} `json:"topics"`
	}](t, rec)

	if resp.TotalChunks != 3 || len(resp.Topics) != 3 {
		t.Fatalf("total=%d topics=%d, want 3/3", resp.TotalChunks, len(resp.Topics))
	}
	if resp.Topics[1].Title != "Naming" {
		t.Fatalf("second topic title = %q", resp.Topics[1].Title)
	}
}

func TestHandlePreviewGitHub(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{fetcher: stubFetcher{content: "# Topic\nbody"}})
	rec := doRequest(t, handler, http.MethodPost, "/api/preview", map[string]any{
		"source": map[string]any{"type": "github_raw", "url": "https://raw.githubusercontent.com/o/r/main/a.md"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	resp := decodeBody[struct {
		Name        string `json:"name"`
		TotalChunks int    `json:"total_chunks"`
	}](t, rec)
	if resp.TotalChunks != 1 || !strings.Contains(resp.Name, "raw.githubusercontent.com") {
		t.Fatalf("unexpected preview: %+v", resp)
	}
}

func TestHandlePreviewGitHubFetchError(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{fetcher: stubFetcher{err: errors.New("404")}})
	rec := doRequest(t, handler, http.MethodPost, "/api/preview", map[string]any{
		"source": map[string]any{"type": "github_raw", "url": "https://raw.githubusercontent.com/o/r/main/a.md"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCollections(t *testing.T) {
	t.Parallel()

	lister := stubLister{collections: []httpapi.Collection{
		{Name: "markdown", Dimension: 384, VectorName: "fast-bge-small-en-v1.5", Points: 12},
	}}
	rec := doRequest(t, newTestServer(t, testDeps{lister: lister}), http.MethodGet, "/api/collections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	resp := decodeBody[struct {
		Collections []httpapi.Collection `json:"collections"`
	}](t, rec)
	if len(resp.Collections) != 1 || resp.Collections[0].Name != "markdown" {
		t.Fatalf("collections = %+v", resp.Collections)
	}
}

func TestHandleCreateCollection(t *testing.T) {
	t.Parallel()

	creator := &stubCreator{}
	model := httpapi.ModelInfo{Dimension: 384, VectorName: "fast-bge-small-en-v1.5"}
	handler := newTestServer(t, testDeps{creator: creator, model: model})

	rec := doRequest(t, handler, http.MethodPost, "/api/collections", map[string]string{"name": "go-guide"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	creator.mu.Lock()
	defer creator.mu.Unlock()
	if len(creator.created) != 1 || creator.created[0] != "go-guide" {
		t.Fatalf("created = %v", creator.created)
	}
}

func TestHandleCreateCollectionValidation(t *testing.T) {
	t.Parallel()

	rec := doRequest(t, newTestServer(t, testDeps{creator: &stubCreator{}}), http.MethodPost, "/api/collections", map[string]string{"name": "  "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleIngestRejectsDimensionMismatch(t *testing.T) {
	t.Parallel()

	lister := stubLister{collections: []httpapi.Collection{
		{Name: "wrong", Dimension: 768, VectorName: "other"},
	}}
	model := httpapi.ModelInfo{Dimension: 384, VectorName: "fast-bge-small-en-v1.5"}
	handler := newTestServer(t, testDeps{lister: lister, model: model})

	body := uploadSource("go.md", "# A\nbody")
	body["collection"] = "wrong"
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestHandleIngestRunsJobToCompletion(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{ingester: stubIngester{ingested: 7}})
	body := uploadSource("go.md", "# A\nbody")
	body["collection"] = "go-guide"
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID

	job := waitForJob(t, handler, jobID)
	if job.State != httpapi.JobSucceeded || job.Ingested != 7 {
		t.Fatalf("job = %+v, want succeeded/7", job)
	}
}

func TestHandleIngestRecordsFailure(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{ingester: stubIngester{err: errors.New("embed boom")}})
	body := uploadSource("go.md", "# A\nbody")
	body["collection"] = "go-guide"
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", body)
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID

	job := waitForJob(t, handler, jobID)
	if job.State != httpapi.JobFailed || job.Error == "" {
		t.Fatalf("job = %+v, want failed with error", job)
	}
}

func TestHandleIngestMissingCollection(t *testing.T) {
	t.Parallel()

	rec := doRequest(t, newTestServer(t, testDeps{}), http.MethodPost, "/api/ingest", uploadSource("go.md", "# A\nbody"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleJobNotFound(t *testing.T) {
	t.Parallel()

	rec := doRequest(t, newTestServer(t, testDeps{}), http.MethodGet, "/api/jobs/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleJobStream(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{ingester: stubIngester{ingested: 4}})
	body := uploadSource("go.md", "# A\nbody")
	body["collection"] = "go-guide"
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, doRequest(t, handler, http.MethodPost, "/api/ingest", body)).JobID

	rec := doRequest(t, handler, http.MethodGet, "/api/jobs/"+jobID+"/stream", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "data:") || !strings.Contains(rec.Body.String(), string(httpapi.JobSucceeded)) {
		t.Fatalf("stream did not contain a terminal event:\n%s", rec.Body.String())
	}
}

func waitForJob(t *testing.T, handler http.Handler, id string) httpapi.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := doRequest(t, handler, http.MethodGet, "/api/jobs/"+id, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("job status = %d", rec.Code)
		}
		job := decodeBody[httpapi.Job](t, rec)
		if job.State == httpapi.JobSucceeded || job.State == httpapi.JobFailed {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not reach a terminal state in time")
	return httpapi.Job{}
}

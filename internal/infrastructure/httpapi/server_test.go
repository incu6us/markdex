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
	"testing/fstest"
	"time"

	"github.com/incu6us/markdex/internal/domain"
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

type stubDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (s *stubDeleter) Delete(_ context.Context, name string) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	s.deleted = append(s.deleted, name)
	s.mu.Unlock()
	return nil
}

type stubRepoLister struct {
	urls   []string
	err    error
	gotURL string
}

func (s *stubRepoLister) ListMarkdown(_ context.Context, repoURL string) ([]string, error) {
	s.gotURL = repoURL
	return s.urls, s.err
}

type capturingIngester struct {
	mu   sync.Mutex
	spec httpapi.IngestSpec
}

func (c *capturingIngester) Ingest(_ context.Context, spec httpapi.IngestSpec, report func(int, int)) (int, error) {
	c.mu.Lock()
	c.spec = spec
	c.mu.Unlock()
	report(len(spec.URLs), len(spec.URLs))
	return len(spec.URLs), nil
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

type stubSearcher struct {
	hits          []domain.SearchHit
	err           error
	gotCollection string
	gotQuery      string
	gotTopK       int
}

func (s *stubSearcher) Search(_ context.Context, collection, query string, topK int, _ domain.Filter, _ bool) ([]domain.SearchHit, error) {
	s.gotCollection = collection
	s.gotQuery = query
	s.gotTopK = topK
	return s.hits, s.err
}

type stubHeadings struct {
	headings []string
	err      error
}

func (s stubHeadings) Headings(context.Context, string) ([]string, error) {
	return s.headings, s.err
}

type testDeps struct {
	lister     httpapi.CollectionLister
	creator    httpapi.CollectionCreator
	deleter    httpapi.CollectionDeleter
	repoLister httpapi.RepoLister
	fetcher    httpapi.Fetcher
	ingester   httpapi.Ingester
	searcher   httpapi.Searcher
	headings   httpapi.HeadingsProvider
	model      httpapi.ModelInfo
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
		Chunker:    markdown.NewSplitter(2000, 200),
		Fetcher:    deps.fetcher,
		Lister:     deps.lister,
		Creator:    deps.creator,
		Deleter:    deps.deleter,
		RepoLister: deps.repoLister,
		Headings:   deps.headings,
		Searcher:   deps.searcher,
		Jobs:       manager,
		Model:      deps.model,
		Logger:     logger,
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

func TestStaticCacheHeaders(t *testing.T) {
	t.Parallel()

	ui := fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := httpapi.NewServer(httpapi.Config{UI: ui, Logger: logger}).Handler()

	cases := []struct {
		name, path, wantCache string
	}{
		{"index root", "/", "no-cache"},
		{"spa fallback route", "/search", "no-cache"},
		{"hashed asset", "/assets/index-abc123.js", "public, max-age=31536000, immutable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("Cache-Control"); got != tc.wantCache {
				t.Fatalf("%s Cache-Control = %q, want %q", tc.path, got, tc.wantCache)
			}
		})
	}
}

func TestHandleDeleteCollection(t *testing.T) {
	t.Parallel()

	deleter := &stubDeleter{}
	handler := newTestServer(t, testDeps{deleter: deleter})

	rec := doRequest(t, handler, http.MethodDelete, "/api/collections/go-guide", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	deleter.mu.Lock()
	defer deleter.mu.Unlock()
	if len(deleter.deleted) != 1 || deleter.deleted[0] != "go-guide" {
		t.Fatalf("deleted = %v", deleter.deleted)
	}
}

func TestHandleDeleteCollectionError(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{deleter: &stubDeleter{err: errors.New("qdrant down")}})
	rec := doRequest(t, handler, http.MethodDelete, "/api/collections/go-guide", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
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

func TestHandleIngestGithubRepo(t *testing.T) {
	t.Parallel()

	urls := []string{"https://raw.example/o/r/main/a.md", "https://raw.example/o/r/main/docs/b.md"}
	lister := &stubRepoLister{urls: urls}
	ingester := &capturingIngester{}
	handler := newTestServer(t, testDeps{repoLister: lister, ingester: ingester})

	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "github_repo", "url": "https://github.com/o/r"},
		"collection": "go-guide",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID
	if job := waitForJob(t, handler, jobID); job.State != httpapi.JobSucceeded {
		t.Fatalf("job = %+v, want succeeded", job)
	}
	if lister.gotURL != "https://github.com/o/r" {
		t.Fatalf("lister got url %q", lister.gotURL)
	}
	ingester.mu.Lock()
	defer ingester.mu.Unlock()
	if len(ingester.spec.URLs) != 2 || ingester.spec.URLs[1] != urls[1] {
		t.Fatalf("ingester spec URLs = %v", ingester.spec.URLs)
	}
}

func TestHandleIngestUploadDir(t *testing.T) {
	t.Parallel()

	ingester := &capturingIngester{}
	handler := newTestServer(t, testDeps{ingester: ingester})

	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source": map[string]any{"type": "upload_dir", "files": []map[string]string{
			{"name": "a.md", "content": "# A\nbody"},
			{"name": "docs/b.md", "content": "# B\nmore"},
		}},
		"collection": "go-guide",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID
	if job := waitForJob(t, handler, jobID); job.State != httpapi.JobSucceeded {
		t.Fatalf("job = %+v, want succeeded", job)
	}
	ingester.mu.Lock()
	defer ingester.mu.Unlock()
	if len(ingester.spec.Files) != 2 || ingester.spec.Files[1].Name != "docs/b.md" {
		t.Fatalf("ingester spec Files = %+v", ingester.spec.Files)
	}
}

func TestHandleIngestUploadDirEmpty(t *testing.T) {
	t.Parallel()
	handler := newTestServer(t, testDeps{})
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "upload_dir", "files": []map[string]string{}},
		"collection": "go-guide",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleIngestGithubRepoPrune(t *testing.T) {
	t.Parallel()

	lister := &stubRepoLister{urls: []string{
		"https://raw.githubusercontent.com/o/r/main/a.md",
		"https://raw.githubusercontent.com/o/r/main/docs/b.md",
	}}
	ingester := &capturingIngester{}
	handler := newTestServer(t, testDeps{repoLister: lister, ingester: ingester})

	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "github_repo", "url": "https://github.com/o/r"},
		"collection": "go-guide",
		"prune":      true,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID
	waitForJob(t, handler, jobID)

	ingester.mu.Lock()
	defer ingester.mu.Unlock()
	if got := ingester.spec.ReconcileScope; got != "https://raw.githubusercontent.com/o/r/" {
		t.Fatalf("ReconcileScope = %q, want the repo prefix", got)
	}
}

func TestHandleIngestGithubRepoNoPrune(t *testing.T) {
	t.Parallel()
	lister := &stubRepoLister{urls: []string{"https://raw.githubusercontent.com/o/r/main/a.md"}}
	ingester := &capturingIngester{}
	handler := newTestServer(t, testDeps{repoLister: lister, ingester: ingester})
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "github_repo", "url": "https://github.com/o/r"},
		"collection": "go-guide",
	})
	jobID := decodeBody[struct {
		JobID string `json:"job_id"`
	}](t, rec).JobID
	waitForJob(t, handler, jobID)
	ingester.mu.Lock()
	defer ingester.mu.Unlock()
	if ingester.spec.ReconcileScope != "" {
		t.Fatalf("ReconcileScope = %q, want empty without prune", ingester.spec.ReconcileScope)
	}
}

func TestHandleIngestGithubRepoListError(t *testing.T) {
	t.Parallel()
	handler := newTestServer(t, testDeps{repoLister: &stubRepoLister{err: errors.New("repo not found")}})
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "github_repo", "url": "https://github.com/o/missing"},
		"collection": "go-guide",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleIngestGithubRepoNotConfigured(t *testing.T) {
	t.Parallel()
	handler := newTestServer(t, testDeps{}) // no repoLister
	rec := doRequest(t, handler, http.MethodPost, "/api/ingest", map[string]any{
		"source":     map[string]any{"type": "github_repo", "url": "https://github.com/o/r"},
		"collection": "go-guide",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
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

func TestHandleSearch(t *testing.T) {
	t.Parallel()

	searcher := &stubSearcher{hits: []domain.SearchHit{
		{ID: "x", Score: 0.9, Document: "doc x", Metadata: map[string]string{"title": "Naming"}},
	}}
	handler := newTestServer(t, testDeps{searcher: searcher})

	rec := doRequest(t, handler, http.MethodPost, "/api/search", map[string]any{
		"collection": "go-guide", "query": "how to name things", "top_k": 5,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	resp := decodeBody[struct {
		Results []struct {
			ID       string            `json:"id"`
			Score    float32           `json:"score"`
			Document string            `json:"document"`
			Metadata map[string]string `json:"metadata"`
		} `json:"results"`
	}](t, rec)

	if len(resp.Results) != 1 || resp.Results[0].ID != "x" || resp.Results[0].Metadata["title"] != "Naming" {
		t.Fatalf("results = %+v", resp.Results)
	}
	if searcher.gotCollection != "go-guide" || searcher.gotQuery != "how to name things" || searcher.gotTopK != 5 {
		t.Fatalf("searcher got collection=%q query=%q topK=%d", searcher.gotCollection, searcher.gotQuery, searcher.gotTopK)
	}
}

func TestHandleSearchValidation(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{searcher: &stubSearcher{}})
	rec := doRequest(t, handler, http.MethodPost, "/api/search", map[string]any{"query": "no collection"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSearchDefaultsTopK(t *testing.T) {
	t.Parallel()

	searcher := &stubSearcher{}
	handler := newTestServer(t, testDeps{searcher: searcher})
	doRequest(t, handler, http.MethodPost, "/api/search", map[string]any{"collection": "c", "query": "q"})
	if searcher.gotTopK != 8 {
		t.Fatalf("default top_k = %d, want 8", searcher.gotTopK)
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

type mapSearcher struct {
	byQuery map[string][]domain.SearchHit
}

func (m mapSearcher) Search(_ context.Context, _, query string, _ int, _ domain.Filter, _ bool) ([]domain.SearchHit, error) {
	return m.byQuery[query], nil
}

func hit(headingPath string) domain.SearchHit {
	return domain.SearchHit{Metadata: map[string]string{"heading_path": headingPath}}
}

func TestHandleEval(t *testing.T) {
	t.Parallel()

	searcher := mapSearcher{byQuery: map[string][]domain.SearchHit{
		"interfaces": {hit("go/slices"), hit("go/interfaces")}, // relevant at rank 2
		"missing":    {hit("go/slices"), hit("go/errors")},     // no relevant
	}}
	handler := newTestServer(t, testDeps{searcher: searcher})

	rec := doRequest(t, handler, http.MethodPost, "/api/eval", map[string]any{
		"collection": "c", "top_k": 5,
		"queries": []map[string]any{
			{"query": "interfaces", "relevant_heading_contains": []string{"interfaces"}},
			{"query": "missing", "relevant_heading_contains": []string{"interfaces"}},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	resp := decodeBody[struct {
		TopK    int `json:"top_k"`
		Metrics struct {
			Queries int     `json:"queries"`
			MRR     float64 `json:"mrr"`
			HitAt1  float64 `json:"hit_at_1"`
			HitAtK  float64 `json:"hit_at_k"`
		} `json:"metrics"`
		Results []struct {
			Query string `json:"query"`
			Rank  int    `json:"rank"`
		} `json:"results"`
	}](t, rec)

	if resp.Metrics.Queries != 2 {
		t.Fatalf("queries = %d", resp.Metrics.Queries)
	}
	if resp.Metrics.HitAt1 != 0 || resp.Metrics.HitAtK != 0.5 {
		t.Fatalf("hit@1=%v hit@k=%v, want 0 / 0.5", resp.Metrics.HitAt1, resp.Metrics.HitAtK)
	}
	if resp.Metrics.MRR != 0.25 { // (1/2 + 0)/2
		t.Fatalf("MRR = %v, want 0.25", resp.Metrics.MRR)
	}
	if len(resp.Results) != 2 || resp.Results[0].Rank != 2 || resp.Results[1].Rank != 0 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestHandleHeadings(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{headings: stubHeadings{headings: []string{"go/interfaces", "go/naming"}}})
	rec := doRequest(t, handler, http.MethodGet, "/api/collections/go/headings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	resp := decodeBody[struct {
		Headings []string `json:"headings"`
	}](t, rec)
	if len(resp.Headings) != 2 || resp.Headings[0] != "go/interfaces" {
		t.Fatalf("headings = %v", resp.Headings)
	}
}

func TestHandleEvalValidation(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, testDeps{searcher: mapSearcher{}})
	rec := doRequest(t, handler, http.MethodPost, "/api/eval", map[string]any{"collection": "c"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no queries)", rec.Code)
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

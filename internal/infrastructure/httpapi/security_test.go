package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Collection names are concatenated into Qdrant request paths by the adapter,
// so anything beyond [A-Za-z0-9._-] must be rejected at the handler boundary
// (path injection, e.g. "x/points/delete?wait=true").
func TestCollectionNameRejectedOnEveryEntryPoint(t *testing.T) {
	// Memory ports get stubs (their handlers 501 on nil before validating);
	// everything else stays nil: if validation fails to short-circuit, the
	// handler panics on a nil port and the test fails.
	handler := newTestServer(t, testDeps{
		memorizer:     &stubMemorizer{},
		memoryLister:  &stubMemoryLister{},
		sourceLister:  &stubSourceLister{},
		sourceDeleter: &stubSourceDeleter{},
	})

	bad := "x/points/delete?wait=true"
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"search", http.MethodPost, "/api/search", map[string]any{"collection": bad, "query": "q"}},
		{"create collection", http.MethodPost, "/api/collections", map[string]any{"name": bad}},
		{"delete collection", http.MethodDelete, "/api/collections/x%2Fpoints", nil},
		{"headings", http.MethodGet, "/api/collections/x%2Fpoints/headings", nil},
		{"list memories", http.MethodGet, "/api/collections/x%2Fpoints/memories", nil},
		{"list sources", http.MethodGet, "/api/collections/x%2Fpoints/sources", nil},
		{"delete source", http.MethodDelete, "/api/collections/x%2Fpoints/sources?source_id=a.md", nil},
		{"ingest", http.MethodPost, "/api/ingest", map[string]any{"collection": bad, "source": map[string]any{"type": "upload", "name": "a.md", "content": "# A\nbody"}}},
		{"remember", http.MethodPost, "/api/memories", map[string]any{"collection": bad, "text": "fact"}},
		{"forget", http.MethodDelete, "/api/memories/id1?collection=" + "x%2Fpoints", nil},
		{"eval", http.MethodPost, "/api/eval", map[string]any{"collection": bad, "queries": []map[string]any{{"query": "q", "relevant_heading_contains": []string{"x"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, handler, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid collection name") {
				t.Fatalf("expected invalid-collection-name error, got: %s", rec.Body.String())
			}
		})
	}
}

func TestValidCollectionNamesPass(t *testing.T) {
	handler := newTestServer(t, testDeps{searcher: &stubSearcher{}})
	for _, name := range []string{"go-style-guide", "dev_memory", "v1.2", "A9"} {
		rec := doRequest(t, handler, http.MethodPost, "/api/search",
			map[string]any{"collection": name, "query": "q"})
		if rec.Code != http.StatusOK {
			t.Fatalf("collection %q: expected 200, got %d (body: %s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	handler := newTestServer(t, testDeps{})

	// > 1 MiB body against a maxBodyBytes endpoint.
	big := strings.Repeat("a", (1<<20)+1024)
	rec := doRequest(t, handler, http.MethodPost, "/api/search",
		map[string]any{"collection": "ok", "query": big})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// With MARKDEX_API_TOKEN configured, every mutating route requires
// Authorization: Bearer <token>; read routes stay open.
func TestTokenAuthGatesMutatingRoutes(t *testing.T) {
	handler := newTestServer(t, testDeps{
		token:         "s3cret",
		creator:       &stubCreator{},
		deleter:       &stubDeleter{},
		memorizer:     &stubMemorizer{},
		memoryLister:  &stubMemoryLister{},
		sourceLister:  &stubSourceLister{},
		sourceDeleter: &stubSourceDeleter{},
		searcher:      &stubSearcher{},
		headings:      stubHeadings{},
	})

	mutating := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"ingest", http.MethodPost, "/api/ingest", map[string]any{"collection": "c", "source": map[string]any{"type": "upload", "name": "a.md", "content": "# A\nbody"}}},
		{"create collection", http.MethodPost, "/api/collections", map[string]any{"name": "c"}},
		{"delete collection", http.MethodDelete, "/api/collections/c", nil},
		{"remember", http.MethodPost, "/api/memories", map[string]any{"collection": "c", "text": "fact"}},
		{"forget", http.MethodDelete, "/api/memories/id1?collection=c", nil},
		{"delete source", http.MethodDelete, "/api/collections/c/sources?source_id=a.md", nil},
		{"eval", http.MethodPost, "/api/eval", map[string]any{"collection": "c", "queries": []map[string]any{{"query": "q"}}}},
	}
	for _, tc := range mutating {
		t.Run(tc.name+" without token", func(t *testing.T) {
			rec := doRequest(t, handler, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (body: %s)", rec.Code, rec.Body.String())
			}
		})
		t.Run(tc.name+" with wrong token", func(t *testing.T) {
			rec := doAuthedRequest(t, handler, tc.method, tc.path, tc.body, "wrong")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (body: %s)", rec.Code, rec.Body.String())
			}
		})
		t.Run(tc.name+" with correct token", func(t *testing.T) {
			rec := doAuthedRequest(t, handler, tc.method, tc.path, tc.body, "s3cret")
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("expected auth to pass, got %d (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}

	// Read routes stay open with a token configured.
	for _, path := range []string{"/api/collections", "/api/collections/c/headings", "/api/collections/c/memories", "/api/collections/c/sources"} {
		rec := doRequest(t, handler, http.MethodGet, path, nil)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("GET %s: read route must stay open, got 401", path)
		}
	}
	rec := doRequest(t, handler, http.MethodPost, "/api/search", map[string]any{"collection": "c", "query": "q"})
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("POST /api/search: read route must stay open, got 401")
	}
}

// Without a configured token the API stays open (single-user default).
func TestNoTokenKeepsAPIOpen(t *testing.T) {
	handler := newTestServer(t, testDeps{creator: &stubCreator{}})
	rec := doRequest(t, handler, http.MethodPost, "/api/collections", map[string]any{"name": "c"})
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected open API without token, got 401")
	}
}

func doAuthedRequest(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
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
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

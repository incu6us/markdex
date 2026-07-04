package httpapi_test

import (
	"net/http"
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
		memorizer:    &stubMemorizer{},
		memoryLister: &stubMemoryLister{},
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

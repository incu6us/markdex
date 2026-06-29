package markdexclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client())
}

func TestSearch(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod string
	var gotBody struct {
		Collection string `json:"collection"`
		Query      string `json:"query"`
		TopK       int    `json:"top_k"`
		Expand     bool   `json:"expand"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"id":"1","score":0.91,"document":"body one","metadata":{"heading_path":"go/naming"}},
			{"id":"2","score":0.42,"document":"body two","metadata":{"heading_path":"go/errors"}}
		]}`))
	})

	hits, err := c.Search(context.Background(), SearchParams{Collection: "c", Query: "naming", TopK: 5, Expand: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/search" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody.Collection != "c" || gotBody.Query != "naming" || gotBody.TopK != 5 || !gotBody.Expand {
		t.Fatalf("request body = %+v", gotBody)
	}
	if len(hits) != 2 || hits[0].Score != 0.91 || hits[0].Metadata["heading_path"] != "go/naming" || hits[1].Document != "body two" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchNon200(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"collection not found"}`, http.StatusBadGateway)
	})
	_, err := c.Search(context.Background(), SearchParams{Collection: "c", Query: "q"})
	if err == nil {
		t.Fatal("expected error on non-200, got nil")
	}
	// The backend's message must be surfaced so an agent can self-correct.
	if !strings.Contains(err.Error(), "collection not found") {
		t.Fatalf("error should include backend body, got %q", err.Error())
	}
}

func TestListCollections(t *testing.T) {
	t.Parallel()

	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"collections":[
			{"name":"go-style-guide","dimension":1024,"vector_name":"dense","points":115}
		]}`))
	})

	cols, err := c.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if gotPath != "/api/collections" {
		t.Fatalf("path = %s", gotPath)
	}
	if len(cols) != 1 || cols[0].Name != "go-style-guide" || cols[0].Dimension != 1024 || cols[0].Points != 115 {
		t.Fatalf("collections = %+v", cols)
	}
}

func TestRemember(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod string
	var gotBody struct {
		Collection string `json:"collection"`
		Text       string `json:"text"`
		Author     string `json:"author"`
		Tags       string `json:"tags"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"source_id":"memory:abc","superseded":true,"version":3}`))
	})

	res, err := c.Remember(context.Background(), RememberParams{
		Collection: "team-memory", Text: "a fact", Author: "agent:claude-code", Tags: "billing",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/memories" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody.Collection != "team-memory" || gotBody.Text != "a fact" || gotBody.Author != "agent:claude-code" || gotBody.Tags != "billing" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if res.SourceID != "memory:abc" || !res.Superseded || res.Version != 3 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRememberWithThreshold(t *testing.T) {
	t.Parallel()

	var raw map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"source_id":"memory:abc"}`))
	})

	th := 0.9
	if _, err := c.Remember(context.Background(), RememberParams{Collection: "c", Text: "x", SupersedeThreshold: &th}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if raw["supersede_threshold"] != 0.9 {
		t.Fatalf("body supersede_threshold = %v, want 0.9", raw["supersede_threshold"])
	}
}

func TestRememberOmitsThresholdWhenUnset(t *testing.T) {
	t.Parallel()

	var raw map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"source_id":"memory:abc"}`))
	})

	if _, err := c.Remember(context.Background(), RememberParams{Collection: "c", Text: "x"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, present := raw["supersede_threshold"]; present {
		t.Fatalf("supersede_threshold should be omitted when unset, body=%v", raw)
	}
}

func TestForget(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotQuery = r.URL.Path, r.Method, r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.Forget(context.Background(), "team-memory", "memory:abc"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/memories/memory:abc" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotQuery != "collection=team-memory" {
		t.Fatalf("query = %q, want collection=team-memory", gotQuery)
	}
}

func TestRememberNon2xx(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"collection mismatch"}`, http.StatusConflict)
	})
	_, err := c.Remember(context.Background(), RememberParams{Collection: "c", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "collection mismatch") {
		t.Fatalf("err = %v, want surfaced backend message", err)
	}
}

func TestListHeadings(t *testing.T) {
	t.Parallel()

	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"headings":["go/naming","go/errors"]}`))
	})

	headings, err := c.ListHeadings(context.Background(), "go style guide")
	if err != nil {
		t.Fatalf("ListHeadings: %v", err)
	}
	if gotPath != "/api/collections/go style guide/headings" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(headings) != 2 || headings[0] != "go/naming" {
		t.Fatalf("headings = %+v", headings)
	}
}

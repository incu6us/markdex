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

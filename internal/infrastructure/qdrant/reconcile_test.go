package qdrant_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/qdrant"
)

func TestListSources(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/points/scroll") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"result":{"points":[
			{"payload":{"metadata":{"source_id":"b.md"}}},
			{"payload":{"metadata":{"source_id":"a.md"}}},
			{"payload":{"metadata":{"source_id":"a.md"}}}
		],"next_page_offset":null}}`))
	}))
	t.Cleanup(server.Close)

	repo := qdrant.NewRepository(server.URL, "", testCollection, testSchema)
	sources, err := repo.ListSources(context.Background())
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(sources) != 2 || sources[0] != "a.md" || sources[1] != "b.md" {
		t.Fatalf("sources = %v, want distinct+sorted [a.md b.md]", sources)
	}
}

func TestDeleteSources(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	repo := qdrant.NewRepository(server.URL, "", testCollection, testSchema)
	if err := repo.DeleteSources(context.Background(), []string{"gone.md", "old.md"}); err != nil {
		t.Fatalf("DeleteSources: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.Contains(gotPath, "/points/delete") {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	// the filter must target both source_ids via match-any
	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), "gone.md") || !strings.Contains(string(raw), "old.md") {
		t.Fatalf("delete filter = %s", raw)
	}
}

func TestDeleteSourcesEmptyNoCall(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not call qdrant for empty source list")
	}))
	t.Cleanup(server.Close)
	repo := qdrant.NewRepository(server.URL, "", testCollection, testSchema)
	if err := repo.DeleteSources(context.Background(), nil); err != nil {
		t.Fatalf("DeleteSources(nil) = %v", err)
	}
}

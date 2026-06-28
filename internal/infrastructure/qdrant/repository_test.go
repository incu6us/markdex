package qdrant_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
	"github.com/incu6us/markdex/internal/infrastructure/qdrant"
)

const (
	testCollection = "markdown"
	testVectorName = "fast-test"
)

type recordedRequest struct {
	method string
	path   string
	body   map[string]any
}

type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
	exists   bool
}

func (rec *recorder) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
		}

		rec.mu.Lock()
		rec.requests = append(rec.requests, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
		rec.mu.Unlock()

		if strings.HasSuffix(r.URL.Path, "/exists") {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"exists": rec.exists}})
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func newRepository(t *testing.T, exists bool) (*qdrant.Repository, *recorder) {
	t.Helper()
	rec := &recorder{exists: exists}
	server := httptest.NewServer(rec.handler(t))
	t.Cleanup(server.Close)
	return qdrant.NewRepository(server.URL, "", testCollection, testVectorName), rec
}

func (rec *recorder) calls() []recordedRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]recordedRequest(nil), rec.requests...)
}

func embeddedChunk(t *testing.T, sourceID string, index int) domain.EmbeddedChunk {
	t.Helper()
	chunk, err := domain.NewChunk(domain.ChunkParams{
		SourceID:    sourceID,
		Index:       index,
		Title:       "Naming",
		HeadingPath: "naming",
		Content:     "keep names short",
	})
	if err != nil {
		t.Fatalf("new chunk: %v", err)
	}
	embedding, err := domain.NewEmbedding([]float32{0.1, 0.2, 0.3})
	if err != nil {
		t.Fatalf("new embedding: %v", err)
	}
	return domain.EmbeddedChunk{Chunk: chunk, Embedding: embedding}
}

func TestRepositoryReplaceDeletesThenUpserts(t *testing.T) {
	t.Parallel()

	repo, rec := newRepository(t, true)
	chunks := []domain.EmbeddedChunk{
		embeddedChunk(t, "docs/go.md", 0),
		embeddedChunk(t, "docs/go.md", 1),
	}

	if err := repo.Replace(context.Background(), "docs/go.md", chunks); err != nil {
		t.Fatalf("replace: %v", err)
	}

	calls := rec.calls()
	if len(calls) != 2 {
		t.Fatalf("got %d requests, want 2 (delete then upsert)", len(calls))
	}

	del := calls[0]
	if del.method != http.MethodPost || !strings.HasSuffix(del.path, "/points/delete") {
		t.Fatalf("first request = %s %s, want POST .../points/delete", del.method, del.path)
	}
	if got := sourceIDFromFilter(t, del.body); got != "docs/go.md" {
		t.Fatalf("delete filter source_id = %q, want docs/go.md", got)
	}

	up := calls[1]
	if up.method != http.MethodPut || !strings.HasSuffix(up.path, "/points") {
		t.Fatalf("second request = %s %s, want PUT .../points", up.method, up.path)
	}
	points, ok := up.body["points"].([]any)
	if !ok || len(points) != 2 {
		t.Fatalf("upsert points = %v, want 2", up.body["points"])
	}

	first := points[0].(map[string]any)
	if _, ok := first["id"].(string); !ok {
		t.Fatalf("point missing string id: %v", first)
	}
	vector := first["vector"].(map[string]any)
	if _, ok := vector[testVectorName]; !ok {
		t.Fatalf("point vector missing named vector %q: %v", testVectorName, vector)
	}
	payload := first["payload"].(map[string]any)
	if payload["document"] != "keep names short" {
		t.Fatalf("payload document = %v", payload["document"])
	}
	metadata := payload["metadata"].(map[string]any)
	for _, key := range []string{"path", "source_id", "title", "heading_path", "chunk_index"} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("payload metadata missing %q: %v", key, metadata)
		}
	}
	if metadata["source_id"] != "docs/go.md" {
		t.Fatalf("metadata source_id = %v", metadata["source_id"])
	}
}

func TestRepositoryReplaceWithoutChunksOnlyDeletes(t *testing.T) {
	t.Parallel()

	repo, rec := newRepository(t, true)
	if err := repo.Replace(context.Background(), "docs/go.md", nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d requests, want 1 (delete only)", len(calls))
	}
	if !strings.HasSuffix(calls[0].path, "/points/delete") {
		t.Fatalf("request path = %s, want .../points/delete", calls[0].path)
	}
}

func TestRepositoryPrepare(t *testing.T) {
	t.Parallel()

	t.Run("creates collection when missing", func(t *testing.T) {
		t.Parallel()
		repo, rec := newRepository(t, false)
		if err := repo.Prepare(context.Background(), 384); err != nil {
			t.Fatalf("prepare: %v", err)
		}

		var create *recordedRequest
		for _, call := range rec.calls() {
			if call.method == http.MethodPut && strings.HasSuffix(call.path, "/collections/"+testCollection) {
				c := call
				create = &c
			}
		}
		if create == nil {
			t.Fatal("expected a PUT to create the collection")
		}
		vectors := create.body["vectors"].(map[string]any)
		named := vectors[testVectorName].(map[string]any)
		if named["size"].(float64) != 384 {
			t.Fatalf("vector size = %v, want 384", named["size"])
		}
	})

	t.Run("skips creation when collection exists", func(t *testing.T) {
		t.Parallel()
		repo, rec := newRepository(t, true)
		if err := repo.Prepare(context.Background(), 384); err != nil {
			t.Fatalf("prepare: %v", err)
		}

		for _, call := range rec.calls() {
			if call.method == http.MethodPut && strings.HasSuffix(call.path, "/collections/"+testCollection) {
				t.Fatalf("unexpected collection creation: %v", call)
			}
		}
	})
}

func TestRepositoryList(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"collections": []map[string]any{{"name": "markdown"}, {"name": "go-guide"}},
				},
			})
		case "/collections/markdown", "/collections/go-guide":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"points_count": 12,
					"config": map[string]any{
						"params": map[string]any{
							"vectors": map[string]any{testVectorName: map[string]any{"size": 384}},
						},
					},
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	repo := qdrant.NewRepository(server.URL, "", "", "")
	infos, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d collections, want 2", len(infos))
	}
	if infos[0].Name != "markdown" || infos[0].Dimension != 384 || infos[0].VectorName != testVectorName || infos[0].Points != 12 {
		t.Fatalf("unexpected info: %+v", infos[0])
	}
}

func sourceIDFromFilter(t *testing.T, body map[string]any) string {
	t.Helper()
	filter, ok := body["filter"].(map[string]any)
	if !ok {
		t.Fatalf("body missing filter: %v", body)
	}
	must, ok := filter["must"].([]any)
	if !ok || len(must) == 0 {
		t.Fatalf("filter missing must: %v", filter)
	}
	condition := must[0].(map[string]any)
	if condition["key"] != "metadata.source_id" {
		t.Fatalf("filter key = %v, want metadata.source_id", condition["key"])
	}
	match := condition["match"].(map[string]any)
	value, _ := match["value"].(string)
	return value
}

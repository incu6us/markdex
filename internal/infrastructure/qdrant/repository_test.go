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

const testCollection = "markdown"

var testSchema = domain.CollectionSchema{
	DenseDimension: 384,
	DenseVector:    "bge-m3-dense",
	SparseVector:   "bge-m3-sparse",
}

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

		switch {
		case strings.HasSuffix(r.URL.Path, "/exists"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"exists": rec.exists}})
		case strings.HasSuffix(r.URL.Path, "/points/query"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": []map[string]any{
				{"id": "id-1", "score": 0.7, "payload": map[string]any{
					"document": "hello world",
					"metadata": map[string]any{"source_id": "docs/go.md", "chunk_index": 3, "title": "Naming"},
				}},
			}}})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func newRepository(t *testing.T, exists bool) (*qdrant.Repository, *recorder) {
	t.Helper()
	rec := &recorder{exists: exists}
	server := httptest.NewServer(rec.handler(t))
	t.Cleanup(server.Close)
	return qdrant.NewRepository(server.URL, "", testCollection, testSchema), rec
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
	dense, err := domain.NewEmbedding([]float32{0.1, 0.2, 0.3})
	if err != nil {
		t.Fatalf("new embedding: %v", err)
	}
	sparse, err := domain.NewSparseEmbedding([]uint32{4, 9}, []float32{0.5, 0.6})
	if err != nil {
		t.Fatalf("new sparse: %v", err)
	}
	return domain.EmbeddedChunk{Chunk: chunk, Vectors: domain.Vectors{Dense: dense, Sparse: sparse}}
}

func TestRepositoryReplaceDeletesThenUpserts(t *testing.T) {
	t.Parallel()

	repo, rec := newRepository(t, true)
	chunks := []domain.EmbeddedChunk{embeddedChunk(t, "docs/go.md", 0), embeddedChunk(t, "docs/go.md", 1)}

	if err := repo.Replace(context.Background(), "docs/go.md", chunks); err != nil {
		t.Fatalf("replace: %v", err)
	}

	calls := rec.calls()
	if len(calls) != 2 {
		t.Fatalf("got %d requests, want 2", len(calls))
	}
	if calls[0].method != http.MethodPost || !strings.HasSuffix(calls[0].path, "/points/delete") {
		t.Fatalf("first = %s %s", calls[0].method, calls[0].path)
	}
	if sourceIDFromFilter(t, calls[0].body) != "docs/go.md" {
		t.Fatalf("delete filter source_id wrong")
	}

	up := calls[1]
	if up.method != http.MethodPut || !strings.HasSuffix(up.path, "/points") {
		t.Fatalf("second = %s %s", up.method, up.path)
	}
	points := up.body["points"].([]any)
	if len(points) != 2 {
		t.Fatalf("points = %d, want 2", len(points))
	}
	vector := points[0].(map[string]any)["vector"].(map[string]any)
	if _, ok := vector[testSchema.DenseVector]; !ok {
		t.Fatalf("missing dense vector: %v", vector)
	}
	sparse, ok := vector[testSchema.SparseVector].(map[string]any)
	if !ok || sparse["indices"] == nil || sparse["values"] == nil {
		t.Fatalf("missing/invalid sparse vector: %v", vector)
	}
}

func TestRepositoryPrepare(t *testing.T) {
	t.Parallel()

	t.Run("creates dense+sparse collection when missing", func(t *testing.T) {
		t.Parallel()
		repo, rec := newRepository(t, false)
		if err := repo.Prepare(context.Background()); err != nil {
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
		dense := create.body["vectors"].(map[string]any)[testSchema.DenseVector].(map[string]any)
		if dense["size"].(float64) != 384 {
			t.Fatalf("dense size = %v", dense["size"])
		}
		if _, ok := create.body["sparse_vectors"].(map[string]any)[testSchema.SparseVector]; !ok {
			t.Fatalf("missing sparse_vectors: %v", create.body["sparse_vectors"])
		}
	})

	t.Run("skips creation when collection exists", func(t *testing.T) {
		t.Parallel()
		repo, rec := newRepository(t, true)
		if err := repo.Prepare(context.Background()); err != nil {
			t.Fatalf("prepare: %v", err)
		}
		for _, call := range rec.calls() {
			if call.method == http.MethodPut && strings.HasSuffix(call.path, "/collections/"+testCollection) {
				t.Fatalf("unexpected creation: %v", call)
			}
		}
	})
}

func TestRepositorySearch(t *testing.T) {
	t.Parallel()

	repo, rec := newRepository(t, true)
	dense, _ := domain.NewEmbedding([]float32{0.1, 0.2, 0.3})
	sparse, _ := domain.NewSparseEmbedding([]uint32{1}, []float32{0.9})
	query := domain.Vectors{Dense: dense, Sparse: sparse}

	hits, err := repo.Search(context.Background(), query, 50, domain.Filter{Match: map[string]string{"source_id": "docs/go.md"}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(hits) != 1 || hits[0].ID != "id-1" || hits[0].Document != "hello world" {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Metadata["chunk_index"] != "3" || hits[0].Metadata["title"] != "Naming" {
		t.Fatalf("metadata = %v", hits[0].Metadata)
	}

	var queryReq *recordedRequest
	for _, call := range rec.calls() {
		if strings.HasSuffix(call.path, "/points/query") {
			c := call
			queryReq = &c
		}
	}
	if queryReq == nil {
		t.Fatal("expected a points/query request")
	}
	prefetch := queryReq.body["prefetch"].([]any)
	if len(prefetch) != 2 {
		t.Fatalf("prefetch = %d, want 2 (dense+sparse)", len(prefetch))
	}
	usings := map[string]bool{}
	for _, p := range prefetch {
		usings[p.(map[string]any)["using"].(string)] = true
	}
	if !usings[testSchema.DenseVector] || !usings[testSchema.SparseVector] {
		t.Fatalf("prefetch usings = %v", usings)
	}
	if queryReq.body["query"].(map[string]any)["fusion"] != "rrf" {
		t.Fatalf("fusion = %v", queryReq.body["query"])
	}
	if queryReq.body["filter"] == nil {
		t.Fatal("expected filter in query body")
	}
}

func TestRepositoryList(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"collections": []map[string]any{{"name": "markdown"}}},
			})
		case "/collections/markdown":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"points_count": 12,
					"config": map[string]any{"params": map[string]any{
						"vectors": map[string]any{testSchema.DenseVector: map[string]any{"size": 384}},
					}},
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	repo := qdrant.NewRepository(server.URL, "", "", domain.CollectionSchema{})
	infos, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "markdown" || infos[0].Dimension != 384 || infos[0].VectorName != testSchema.DenseVector || infos[0].Points != 12 {
		t.Fatalf("info = %+v", infos)
	}
}

func sourceIDFromFilter(t *testing.T, body map[string]any) string {
	t.Helper()
	filter := body["filter"].(map[string]any)
	must := filter["must"].([]any)
	condition := must[0].(map[string]any)
	if condition["key"] != "metadata.source_id" {
		t.Fatalf("filter key = %v", condition["key"])
	}
	return condition["match"].(map[string]any)["value"].(string)
}

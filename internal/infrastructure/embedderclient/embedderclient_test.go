package embedderclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
	"github.com/incu6us/markdex/internal/infrastructure/embedderclient"
)

func newClient(t *testing.T, handler http.HandlerFunc) *embedderclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return embedderclient.New(server.URL)
}

func TestEmbed(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody struct {
		Texts []string `json:"texts"`
		Kind  string   `json:"kind"`
	}
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dense": [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
			"sparse": []map[string]any{
				{"indices": []uint32{1, 5}, "values": []float32{0.9, 0.8}},
				{"indices": []uint32{}, "values": []float32{}},
			},
		})
	})

	vectors, err := client.Embed(context.Background(), []string{"alpha", "beta"}, domain.QueryKind)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	if gotPath != "/embed" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.Kind != "query" {
		t.Fatalf("kind = %q, want query", gotBody.Kind)
	}
	if len(gotBody.Texts) != 2 || gotBody.Texts[0] != "alpha" {
		t.Fatalf("texts = %v", gotBody.Texts)
	}

	if len(vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vectors))
	}
	if vectors[0].Dense.Dimension() != 3 {
		t.Fatalf("dense dim = %d, want 3", vectors[0].Dense.Dimension())
	}
	if vectors[0].Sparse.Len() != 2 || vectors[0].Sparse.Indices()[1] != 5 {
		t.Fatalf("sparse[0] = %v / %v", vectors[0].Sparse.Indices(), vectors[0].Sparse.Values())
	}
	if !vectors[1].Sparse.IsEmpty() {
		t.Fatalf("sparse[1] should be empty")
	}
}

func TestEmbedEmptyTextsSkipsCall(t *testing.T) {
	t.Parallel()

	called := false
	client := newClient(t, func(http.ResponseWriter, *http.Request) { called = true })

	vectors, err := client.Embed(context.Background(), nil, domain.DocumentKind)
	if err != nil || vectors != nil {
		t.Fatalf("got %v / %v", vectors, err)
	}
	if called {
		t.Fatal("expected no HTTP call for empty texts")
	}
}

func TestEmbedCountMismatch(t *testing.T) {
	t.Parallel()

	client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dense":  [][]float32{{0.1, 0.2}},
			"sparse": []map[string]any{{"indices": []uint32{}, "values": []float32{}}},
		})
	})

	if _, err := client.Embed(context.Background(), []string{"a", "b"}, domain.DocumentKind); err == nil {
		t.Fatal("expected error on count mismatch")
	}
}

func TestCountTokens(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody struct {
		Texts []string `json:"texts"`
	}
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"counts":[7,512],"max_length":8192}`))
	})

	counts, err := client.CountTokens(context.Background(), []string{"short", "much longer text"})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if gotPath != "/tokenize" {
		t.Fatalf("path = %s", gotPath)
	}
	if len(gotBody.Texts) != 2 || gotBody.Texts[1] != "much longer text" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if len(counts) != 2 || counts[0] != 7 || counts[1] != 512 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestCountTokensEmptySkipsCall(t *testing.T) {
	t.Parallel()
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call embedder for empty texts")
	})
	counts, err := client.CountTokens(context.Background(), nil)
	if err != nil || counts != nil {
		t.Fatalf("got %v / %v, want nil/nil", counts, err)
	}
}

func TestRerank(t *testing.T) {
	t.Parallel()

	var gotBody struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
		TopK      *int     `json:"top_k"`
	}
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 2, "score": 0.91},
				{"index": 0, "score": 0.40},
			},
		})
	})

	ranked, err := client.Rerank(context.Background(), "q", []string{"d0", "d1", "d2"}, 2)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if gotBody.TopK == nil || *gotBody.TopK != 2 {
		t.Fatalf("top_k = %v, want 2", gotBody.TopK)
	}
	if len(ranked) != 2 || ranked[0].Index != 2 || ranked[0].Score != 0.91 {
		t.Fatalf("ranked = %+v", ranked)
	}
}

func TestRerankEmptyDocsSkipsCall(t *testing.T) {
	t.Parallel()

	called := false
	client := newClient(t, func(http.ResponseWriter, *http.Request) { called = true })

	ranked, err := client.Rerank(context.Background(), "q", nil, 5)
	if err != nil || ranked != nil {
		t.Fatalf("got %v / %v", ranked, err)
	}
	if called {
		t.Fatal("expected no HTTP call for empty documents")
	}
}

func TestInfo(t *testing.T) {
	t.Parallel()

	client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dense_dim": 1024, "dense_name": "bge-m3-dense", "sparse_name": "bge-m3-sparse",
			"embed_model": "BAAI/bge-m3", "rerank_model": "BAAI/bge-reranker-v2-m3",
		})
	})

	info, err := client.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.DenseDim != 1024 || info.DenseName != "bge-m3-dense" || info.SparseName != "bge-m3-sparse" {
		t.Fatalf("info = %+v", info)
	}
}

func TestErrorStatus(t *testing.T) {
	t.Parallel()

	client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	if _, err := client.Rerank(context.Background(), "q", []string{"d"}, 0); err == nil {
		t.Fatal("expected error on 500 status")
	}
}

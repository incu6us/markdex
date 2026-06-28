package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/incu6us/markdex/internal/application"
	"github.com/incu6us/markdex/internal/domain"
)

type fakeReranker struct {
	rerank func(query string, documents []string, topK int) ([]domain.Ranked, error)
}

func (f *fakeReranker) Rerank(_ context.Context, query string, documents []string, topK int) ([]domain.Ranked, error) {
	return f.rerank(query, documents, topK)
}

func TestSearchServiceReranksCandidates(t *testing.T) {
	t.Parallel()

	repo := newFakeRepository()
	repo.searchHits = []domain.SearchHit{
		{ID: "a", Score: 0.1, Document: "doc a"},
		{ID: "b", Score: 0.2, Document: "doc b"},
		{ID: "c", Score: 0.3, Document: "doc c"},
	}
	// Reranker reverses the ANN order and assigns new scores, keeping top 2.
	reranker := &fakeReranker{rerank: func(_ string, documents []string, topK int) ([]domain.Ranked, error) {
		if len(documents) != 3 {
			t.Fatalf("reranker got %d documents, want 3", len(documents))
		}
		ranked := []domain.Ranked{{Index: 2, Score: 0.99}, {Index: 1, Score: 0.88}, {Index: 0, Score: 0.05}}
		return ranked[:topK], nil
	}}

	service := application.NewSearchService(&fakeEmbedder{dimension: 8}, repo, reranker, 50, nil)
	hits, err := service.Search(context.Background(), "q", 2, domain.Filter{}, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if repo.searchTopN != 50 {
		t.Fatalf("repo searched with topN=%d, want 50 (pool size)", repo.searchTopN)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (topK)", len(hits))
	}
	if hits[0].ID != "c" || hits[0].Score != 0.99 {
		t.Fatalf("hit[0] = %+v, want c with rerank score 0.99", hits[0])
	}
	if hits[1].ID != "b" {
		t.Fatalf("hit[1] = %+v, want b", hits[1])
	}
}

func TestSearchServiceExpandsToSection(t *testing.T) {
	t.Parallel()

	repo := newFakeRepository()
	repo.searchHits = []domain.SearchHit{
		{ID: "a", Document: "small chunk", Metadata: map[string]string{"source_id": "s", "heading_path": "h"}},
	}
	repo.sectionText = "the full reassembled section text"
	reranker := &fakeReranker{rerank: func(_ string, _ []string, _ int) ([]domain.Ranked, error) {
		return []domain.Ranked{{Index: 0, Score: 0.9}}, nil
	}}
	service := application.NewSearchService(&fakeEmbedder{dimension: 8}, repo, reranker, 50, nil)

	hits, err := service.Search(context.Background(), "q", 1, domain.Filter{}, true)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Document != "the full reassembled section text" {
		t.Fatalf("expand: hit document = %q", hits[0].Document)
	}
}

func TestSearchServiceLogsAndFallsBackWhenExpandFails(t *testing.T) {
	t.Parallel()

	repo := newFakeRepository()
	repo.searchHits = []domain.SearchHit{
		{ID: "a", Document: "small chunk", Metadata: map[string]string{"source_id": "s", "heading_path": "h"}},
	}
	repo.sectionErr = errors.New("scroll failed")
	reranker := &fakeReranker{rerank: func(string, []string, int) ([]domain.Ranked, error) {
		return []domain.Ranked{{Index: 0, Score: 0.9}}, nil
	}}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	service := application.NewSearchService(&fakeEmbedder{dimension: 8}, repo, reranker, 50, logger)

	hits, err := service.Search(context.Background(), "q", 1, domain.Filter{}, true)
	if err != nil {
		t.Fatalf("expand failure must not fail the whole search: %v", err)
	}
	if len(hits) != 1 || hits[0].Document != "small chunk" {
		t.Fatalf("should fall back to the matched chunk, got %q", hits[0].Document)
	}
	if !strings.Contains(buf.String(), "scroll failed") {
		t.Fatalf("section error should be logged, got %q", buf.String())
	}
}

func TestSearchServiceEmptyCandidates(t *testing.T) {
	t.Parallel()

	repo := newFakeRepository() // no searchHits
	reranker := &fakeReranker{rerank: func(string, []string, int) ([]domain.Ranked, error) {
		t.Fatal("reranker should not be called with no candidates")
		return nil, nil
	}}
	service := application.NewSearchService(&fakeEmbedder{dimension: 8}, repo, reranker, 50, nil)

	hits, err := service.Search(context.Background(), "q", 5, domain.Filter{}, false)
	if err != nil || hits != nil {
		t.Fatalf("got %v / %v, want nil/nil", hits, err)
	}
}

func TestSearchServicePropagatesSearchError(t *testing.T) {
	t.Parallel()

	repo := newFakeRepository()
	repo.searchErr = errors.New("qdrant down")
	reranker := &fakeReranker{rerank: func(string, []string, int) ([]domain.Ranked, error) { return nil, nil }}
	service := application.NewSearchService(&fakeEmbedder{dimension: 8}, repo, reranker, 50, nil)

	if _, err := service.Search(context.Background(), "q", 5, domain.Filter{}, false); !errors.Is(err, repo.searchErr) {
		t.Fatalf("err = %v, want %v", err, repo.searchErr)
	}
}

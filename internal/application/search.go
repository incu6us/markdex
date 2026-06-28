package application

import (
	"context"

	"github.com/incu6us/markdex/internal/domain"
)

const defaultPoolSize = 50

type SearchService struct {
	embedder domain.Embedder
	repo     domain.VectorRepository
	reranker domain.Reranker
	poolSize int
}

func NewSearchService(
	embedder domain.Embedder,
	repo domain.VectorRepository,
	reranker domain.Reranker,
	poolSize int,
) *SearchService {
	if poolSize < 1 {
		poolSize = defaultPoolSize
	}
	return &SearchService{
		embedder: embedder,
		repo:     repo,
		reranker: reranker,
		poolSize: poolSize,
	}
}

func (s *SearchService) Search(ctx context.Context, query string, topK int, filter domain.Filter, expand bool) ([]domain.SearchHit, error) {
	if topK < 1 {
		topK = 1
	}

	vectors, err := s.embedder.Embed(ctx, []string{query}, domain.QueryKind)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}

	candidates, err := s.repo.Search(ctx, vectors[0], s.poolSize, filter)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	documents := make([]string, len(candidates))
	for i, candidate := range candidates {
		documents[i] = candidate.Document
	}

	ranked, err := s.reranker.Rerank(ctx, query, documents, topK)
	if err != nil {
		return nil, err
	}

	hits := make([]domain.SearchHit, 0, len(ranked))
	for _, r := range ranked {
		if r.Index < 0 || r.Index >= len(candidates) {
			continue
		}
		hit := candidates[r.Index]
		hit.Score = r.Score
		if expand {
			if section, err := s.repo.Section(ctx, hit.Metadata["source_id"], hit.Metadata["heading_path"]); err == nil && section != "" {
				hit.Document = section
			}
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

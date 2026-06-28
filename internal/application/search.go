package application

import (
	"context"
	"log/slog"

	"github.com/incu6us/markdex/internal/domain"
)

const defaultPoolSize = 50

type SearchService struct {
	embedder domain.Embedder
	repo     domain.VectorRepository
	reranker domain.Reranker
	poolSize int
	logger   *slog.Logger
}

func NewSearchService(
	embedder domain.Embedder,
	repo domain.VectorRepository,
	reranker domain.Reranker,
	poolSize int,
	logger *slog.Logger,
) *SearchService {
	if poolSize < 1 {
		poolSize = defaultPoolSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SearchService{
		embedder: embedder,
		repo:     repo,
		reranker: reranker,
		poolSize: poolSize,
		logger:   logger,
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

	// Rerank against the same contextual text (heading-path breadcrumb + content)
	// that was embedded, so the cross-encoder can separate near-identical sections.
	// The returned hit keeps its raw Document.
	documents := make([]string, len(candidates))
	for i, candidate := range candidates {
		documents[i] = domain.ContextualText(candidate.Metadata["heading_path"], candidate.Document)
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
			// Expansion is best-effort: on failure, log and keep the matched chunk
			// rather than failing the whole search.
			section, err := s.repo.Section(ctx, hit.Metadata["source_id"], hit.Metadata["heading_path"])
			switch {
			case err != nil:
				s.logger.Warn("expand: section reassembly failed; keeping chunk",
					"source_id", hit.Metadata["source_id"],
					"heading_path", hit.Metadata["heading_path"],
					"err", err)
			case section != "":
				hit.Document = section
			}
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

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

// Search retrieves topK hits. rerank=false skips the cross-encoder: it is ~90%
// of search latency on CPU, and when the pool is no larger than topK it can only
// reorder what fusion already picked, never improve the selection.
func (s *SearchService) Search(ctx context.Context, query string, topK int, filter domain.Filter, expand, rerank bool) ([]domain.SearchHit, error) {
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

	// The pool exists only to give the cross-encoder something to choose from.
	// Without reranking, over-fetching would just truncate an arbitrary tail, so
	// ask the index for exactly what the caller wants.
	poolSize := s.poolSize
	if !rerank {
		poolSize = topK
	}
	candidates, err := s.repo.Search(ctx, vectors[0], poolSize, filter)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	var ranked []domain.Ranked
	if rerank {
		// Rerank against the same contextual text (heading-path breadcrumb + content)
		// that was embedded, so the cross-encoder can separate near-identical sections.
		// The returned hit keeps its raw Document.
		documents := make([]string, len(candidates))
		for i, candidate := range candidates {
			documents[i] = domain.ContextualText(candidate.Metadata["heading_path"], candidate.Document)
		}
		if ranked, err = s.reranker.Rerank(ctx, query, documents, topK); err != nil {
			return nil, err
		}
	} else {
		// Keep hybrid-fusion order and its similarity scores. Note these are on a
		// different scale than the cross-encoder's 0..1 — anything thresholding on
		// Score must not mix the two.
		limit := min(topK, len(candidates))
		ranked = make([]domain.Ranked, limit)
		for i := range limit {
			ranked[i] = domain.Ranked{Index: i, Score: candidates[i].Score}
		}
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

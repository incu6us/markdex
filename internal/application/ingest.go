package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/incu6us/markdex/internal/domain"
)

// maxBudgetPasses bounds the re-split loop that enforces the token budget.
const maxBudgetPasses = 5

type IngestResult struct {
	Ingested int
}

type Option func(*IngestService)

func WithProgress(report func(processed, total int)) Option {
	return func(s *IngestService) {
		if report != nil {
			s.report = report
		}
	}
}

// WithDedup drops near-duplicate chunks (word-shingle Jaccard >= threshold) before
// embedding. A threshold <= 0 leaves dedup disabled.
func WithDedup(threshold float64) Option {
	return func(s *IngestService) { s.dedupThreshold = threshold }
}

// WithTokenBudget re-splits any chunk whose embedded text exceeds maxTokens, using
// real token counts from counter, so every stored chunk fits the model window.
func WithTokenBudget(counter domain.TokenCounter, maxTokens int) Option {
	return func(s *IngestService) {
		if counter != nil && maxTokens > 0 {
			s.tokenCounter = counter
			s.maxTokens = maxTokens
		}
	}
}

type IngestService struct {
	source         domain.DocumentSource
	chunker        domain.Chunker
	embedder       domain.Embedder
	repo           domain.VectorRepository
	batchSize      int
	report         func(processed, total int)
	dedupThreshold float64
	tokenCounter   domain.TokenCounter
	maxTokens      int
}

func NewIngestService(
	source domain.DocumentSource,
	chunker domain.Chunker,
	embedder domain.Embedder,
	repo domain.VectorRepository,
	batchSize int,
	opts ...Option,
) *IngestService {
	if batchSize < 1 {
		batchSize = 1
	}
	service := &IngestService{
		source:    source,
		chunker:   chunker,
		embedder:  embedder,
		repo:      repo,
		batchSize: batchSize,
		report:    func(int, int) {},
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *IngestService) Ingest(ctx context.Context) (IngestResult, error) {
	if err := s.repo.Prepare(ctx); err != nil {
		return IngestResult{}, err
	}

	documents, err := s.source.Load(ctx)
	if err != nil {
		return IngestResult{}, err
	}

	total := 0
	for _, document := range documents {
		ingested, err := s.ingestDocument(ctx, document)
		if err != nil {
			return IngestResult{Ingested: total}, err
		}
		total += ingested
	}
	return IngestResult{Ingested: total}, nil
}

func (s *IngestService) ingestDocument(ctx context.Context, document domain.Document) (int, error) {
	chunks, err := s.chunker.Split(document)
	if err != nil {
		return 0, fmt.Errorf("split %s: %w", document.Path(), err)
	}
	if len(chunks) == 0 {
		return 0, nil
	}

	chunks, err = s.refine(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("refine %s: %w", document.Path(), err)
	}

	embedded, err := s.embedChunks(ctx, chunks)
	if err != nil {
		return 0, err
	}
	if err := s.repo.Replace(ctx, document.Path(), embedded); err != nil {
		return 0, err
	}
	return len(embedded), nil
}

// refine applies near-dup dedup and token-budget enforcement (each only if
// configured) and re-indexes the result so chunk IDs stay unique and ordered.
func (s *IngestService) refine(ctx context.Context, chunks []domain.Chunk) ([]domain.Chunk, error) {
	budgetOn := s.tokenCounter != nil && s.maxTokens > 0
	if s.dedupThreshold <= 0 && !budgetOn {
		return chunks, nil
	}

	if s.dedupThreshold > 0 {
		chunks = domain.DedupeChunks(chunks, s.dedupThreshold)
	}
	if budgetOn {
		var err error
		if chunks, err = s.enforceTokenBudget(ctx, chunks); err != nil {
			return nil, err
		}
	}
	return reindexChunks(chunks)
}

// enforceTokenBudget re-splits any chunk whose embedded (contextual) text exceeds
// maxTokens into rune windows that fit, verified against real token counts.
func (s *IngestService) enforceTokenBudget(ctx context.Context, chunks []domain.Chunk) ([]domain.Chunk, error) {
	for range maxBudgetPasses {
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.ContextualText()
		}
		counts, err := s.tokenCounter.CountTokens(ctx, texts)
		if err != nil {
			return nil, err
		}
		if len(counts) != len(chunks) {
			return nil, fmt.Errorf("token count mismatch: got %d for %d chunks", len(counts), len(chunks))
		}

		over := false
		out := make([]domain.Chunk, 0, len(chunks))
		for i, c := range chunks {
			if counts[i] <= s.maxTokens {
				out = append(out, c)
				continue
			}
			over = true
			pieces, err := splitChunkByRunes(c, counts[i], s.maxTokens)
			if err != nil {
				return nil, err
			}
			out = append(out, pieces...)
		}
		chunks = out
		if !over {
			return chunks, nil
		}
	}
	return chunks, nil // best effort after the bounded number of passes
}

// splitChunkByRunes splits an over-budget chunk's content into rune windows sized,
// from the measured runes-per-token, to land under maxTokens (with headroom).
func splitChunkByRunes(c domain.Chunk, tokenCount, maxTokens int) ([]domain.Chunk, error) {
	runes := []rune(c.Content())
	perToken := float64(len(runes)) / float64(tokenCount)
	window := max(int(perToken*float64(maxTokens)*0.85), 1)

	var pieces []domain.Chunk
	for start := 0; start < len(runes); start += window {
		end := min(start+window, len(runes))
		text := strings.TrimSpace(string(runes[start:end]))
		if text == "" {
			continue
		}
		piece, err := domain.NewChunk(domain.ChunkParams{
			SourceID:    c.SourceID(),
			Index:       c.Index(), // re-indexed later by reindexChunks
			Title:       c.Title(),
			HeadingPath: c.HeadingPath(),
			Content:     text,
		})
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, piece)
	}
	return pieces, nil
}

// reindexChunks reassigns contiguous indices so chunk IDs (sourceID#index) stay
// unique and ordered after dedup/splitting.
func reindexChunks(chunks []domain.Chunk) ([]domain.Chunk, error) {
	out := make([]domain.Chunk, len(chunks))
	for i, c := range chunks {
		nc, err := domain.NewChunk(domain.ChunkParams{
			SourceID:    c.SourceID(),
			Index:       i,
			Title:       c.Title(),
			HeadingPath: c.HeadingPath(),
			Content:     c.Content(),
		})
		if err != nil {
			return nil, err
		}
		out[i] = nc
	}
	return out, nil
}

func (s *IngestService) embedChunks(ctx context.Context, chunks []domain.Chunk) ([]domain.EmbeddedChunk, error) {
	total := len(chunks)
	s.report(0, total)

	embedded := make([]domain.EmbeddedChunk, 0, total)
	for start := 0; start < total; start += s.batchSize {
		end := min(start+s.batchSize, total)
		batch := chunks[start:end]

		// Embed the contextual text (heading-path breadcrumb + content) so the
		// vector encodes where the chunk sits; the stored document stays raw.
		contents := make([]string, len(batch))
		for i, chunk := range batch {
			contents[i] = chunk.ContextualText()
		}

		vectors, err := s.embedder.Embed(ctx, contents, domain.DocumentKind)
		if err != nil {
			return nil, err
		}
		if len(vectors) != len(batch) {
			return nil, fmt.Errorf("embedding count mismatch: got %d for %d chunks", len(vectors), len(batch))
		}

		for i := range batch {
			embedded = append(embedded, domain.EmbeddedChunk{Chunk: batch[i], Vectors: vectors[i]})
		}
		s.report(end, total)
	}
	return embedded, nil
}

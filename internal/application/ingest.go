package application

import (
	"context"
	"fmt"

	"github.com/incu6us/markdex/internal/domain"
)

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

type IngestService struct {
	source    domain.DocumentSource
	chunker   domain.Chunker
	embedder  domain.Embedder
	repo      domain.VectorRepository
	batchSize int
	report    func(processed, total int)
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
	if err := s.repo.Prepare(ctx, s.embedder.Dimension()); err != nil {
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

	embedded, err := s.embedChunks(ctx, chunks)
	if err != nil {
		return 0, err
	}
	if err := s.repo.Replace(ctx, document.Path(), embedded); err != nil {
		return 0, err
	}
	return len(embedded), nil
}

func (s *IngestService) embedChunks(ctx context.Context, chunks []domain.Chunk) ([]domain.EmbeddedChunk, error) {
	total := len(chunks)
	s.report(0, total)

	embedded := make([]domain.EmbeddedChunk, 0, total)
	for start := 0; start < total; start += s.batchSize {
		end := min(start+s.batchSize, total)
		batch := chunks[start:end]

		contents := make([]string, len(batch))
		for i, chunk := range batch {
			contents[i] = chunk.Content()
		}

		embeddings, err := s.embedder.Embed(ctx, contents)
		if err != nil {
			return nil, err
		}
		if len(embeddings) != len(batch) {
			return nil, fmt.Errorf("embedding count mismatch: got %d for %d chunks", len(embeddings), len(batch))
		}

		for i := range batch {
			embedded = append(embedded, domain.EmbeddedChunk{Chunk: batch[i], Embedding: embeddings[i]})
		}
		s.report(end, total)
	}
	return embedded, nil
}

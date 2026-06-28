package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/incu6us/markdex/internal/application"
	"github.com/incu6us/markdex/internal/domain"
)

type fakeSource struct {
	documents []domain.Document
	err       error
}

func (f *fakeSource) Load(context.Context) ([]domain.Document, error) {
	return f.documents, f.err
}

type fakeChunker struct {
	split func(domain.Document) ([]domain.Chunk, error)
}

func (f *fakeChunker) Split(doc domain.Document) ([]domain.Chunk, error) {
	return f.split(doc)
}

type fakeEmbedder struct {
	dimension int
}

func (f *fakeEmbedder) Dimension() int { return f.dimension }

func (f *fakeEmbedder) Embed(_ context.Context, contents []string) ([]domain.Embedding, error) {
	embeddings := make([]domain.Embedding, len(contents))
	for i := range contents {
		embedding, err := domain.NewEmbedding(make([]float32, f.dimension))
		if err != nil {
			return nil, err
		}
		embeddings[i] = embedding
	}
	return embeddings, nil
}

type fakeRepository struct {
	preparedDimension int
	bySource          map[string][]domain.EmbeddedChunk
	replaceCalls      int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{bySource: map[string][]domain.EmbeddedChunk{}}
}

func (r *fakeRepository) Prepare(_ context.Context, dimension int) error {
	r.preparedDimension = dimension
	return nil
}

func (r *fakeRepository) Replace(_ context.Context, sourceID string, chunks []domain.EmbeddedChunk) error {
	r.bySource[sourceID] = chunks
	r.replaceCalls++
	return nil
}

func (r *fakeRepository) stored() int {
	total := 0
	for _, chunks := range r.bySource {
		total += len(chunks)
	}
	return total
}

func document(t *testing.T, path string) domain.Document {
	t.Helper()
	doc, err := domain.NewDocument(path, "# "+path+"\nbody")
	if err != nil {
		t.Fatalf("new document: %v", err)
	}
	return doc
}

func chunksFor(t *testing.T, doc domain.Document, n int) []domain.Chunk {
	t.Helper()
	chunks := make([]domain.Chunk, n)
	for i := range chunks {
		chunk, err := domain.NewChunk(domain.ChunkParams{
			SourceID: doc.Path(),
			Index:    i,
			Content:  fmt.Sprintf("chunk %d of %s", i, doc.Path()),
		})
		if err != nil {
			t.Fatalf("new chunk: %v", err)
		}
		chunks[i] = chunk
	}
	return chunks
}

func fixedChunker(t *testing.T, n int) *fakeChunker {
	return &fakeChunker{split: func(doc domain.Document) ([]domain.Chunk, error) {
		return chunksFor(t, doc, n), nil
	}}
}

func TestIngestServiceIngestChunks(t *testing.T) {
	t.Parallel()

	const dimension = 384

	tests := []struct {
		name          string
		documentCount int
		chunksPerDoc  int
		batchSize     int
	}{
		{name: "one chunk per doc", documentCount: 3, chunksPerDoc: 1, batchSize: 16},
		{name: "many chunks per doc", documentCount: 2, chunksPerDoc: 4, batchSize: 16},
		{name: "chunks across batches", documentCount: 1, chunksPerDoc: 5, batchSize: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			docs := make([]domain.Document, tt.documentCount)
			for i := range docs {
				docs[i] = document(t, fmt.Sprintf("docs/doc-%d.md", i))
			}
			source := &fakeSource{documents: docs}
			repo := newFakeRepository()

			service := application.NewIngestService(
				source, fixedChunker(t, tt.chunksPerDoc), &fakeEmbedder{dimension: dimension}, repo, tt.batchSize,
			)
			result, err := service.Ingest(context.Background())
			if err != nil {
				t.Fatalf("ingest: %v", err)
			}

			want := tt.documentCount * tt.chunksPerDoc
			if result.Ingested != want {
				t.Errorf("ingested = %d, want %d", result.Ingested, want)
			}
			if repo.stored() != want {
				t.Errorf("stored = %d, want %d", repo.stored(), want)
			}
			if repo.preparedDimension != dimension {
				t.Errorf("prepared dimension = %d, want %d", repo.preparedDimension, dimension)
			}
		})
	}
}

func TestIngestServiceReplacesSourceOnReingest(t *testing.T) {
	t.Parallel()

	doc := document(t, "docs/go.md")
	source := &fakeSource{documents: []domain.Document{doc}}
	repo := newFakeRepository()
	chunker := &fakeChunker{}
	service := application.NewIngestService(source, chunker, &fakeEmbedder{dimension: 384}, repo, 16)

	chunker.split = func(d domain.Document) ([]domain.Chunk, error) { return chunksFor(t, d, 5), nil }
	if _, err := service.Ingest(context.Background()); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if repo.stored() != 5 {
		t.Fatalf("after first ingest stored = %d, want 5", repo.stored())
	}

	chunker.split = func(d domain.Document) ([]domain.Chunk, error) { return chunksFor(t, d, 3), nil }
	if _, err := service.Ingest(context.Background()); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if repo.stored() != 3 {
		t.Fatalf("after re-ingest stored = %d, want 3 (no orphans)", repo.stored())
	}
}

func TestIngestServicePropagatesSourceError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("load failed")
	service := application.NewIngestService(
		&fakeSource{err: wantErr}, fixedChunker(t, 1), &fakeEmbedder{dimension: 384}, newFakeRepository(), 16,
	)
	if _, err := service.Ingest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestIngestServicePropagatesChunkerError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("split failed")
	chunker := &fakeChunker{split: func(domain.Document) ([]domain.Chunk, error) { return nil, wantErr }}
	source := &fakeSource{documents: []domain.Document{document(t, "docs/go.md")}}
	service := application.NewIngestService(source, chunker, &fakeEmbedder{dimension: 384}, newFakeRepository(), 16)

	if _, err := service.Ingest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

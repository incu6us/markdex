package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	seen      []string // texts passed to Embed, in order
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string, _ domain.EmbedKind) ([]domain.Vectors, error) {
	f.seen = append(f.seen, texts...)
	vectors := make([]domain.Vectors, len(texts))
	for i := range texts {
		dense, err := domain.NewEmbedding(make([]float32, f.dimension))
		if err != nil {
			return nil, err
		}
		sparse, err := domain.NewSparseEmbedding([]uint32{1}, []float32{0.5})
		if err != nil {
			return nil, err
		}
		vectors[i] = domain.Vectors{Dense: dense, Sparse: sparse}
	}
	return vectors, nil
}

type fakeRepository struct {
	prepared     bool
	bySource     map[string][]domain.EmbeddedChunk
	replaceCalls int
	searchHits   []domain.SearchHit
	searchErr    error
	searchTopN   int
	sectionText  string
	sectionErr   error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{bySource: map[string][]domain.EmbeddedChunk{}}
}

func (r *fakeRepository) Prepare(context.Context) error {
	r.prepared = true
	return nil
}

func (r *fakeRepository) Replace(_ context.Context, sourceID string, chunks []domain.EmbeddedChunk) error {
	r.bySource[sourceID] = chunks
	r.replaceCalls++
	return nil
}

func (r *fakeRepository) Search(_ context.Context, _ domain.Vectors, topN int, _ domain.Filter) ([]domain.SearchHit, error) {
	r.searchTopN = topN
	return r.searchHits, r.searchErr
}

func (r *fakeRepository) Section(_ context.Context, _, _ string) (string, error) {
	return r.sectionText, r.sectionErr
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

func TestIngestEmbedsContextualBreadcrumb(t *testing.T) {
	t.Parallel()

	doc := document(t, "docs/x.md")
	chunker := &fakeChunker{split: func(domain.Document) ([]domain.Chunk, error) {
		c, err := domain.NewChunk(domain.ChunkParams{
			SourceID:    doc.Path(),
			Index:       0,
			HeadingPath: "guide/error-handling/placement-of-w",
			Content:     "wrap with %w",
		})
		if err != nil {
			return nil, err
		}
		return []domain.Chunk{c}, nil
	}}
	emb := &fakeEmbedder{dimension: 8}
	repo := newFakeRepository()
	service := application.NewIngestService(&fakeSource{documents: []domain.Document{doc}}, chunker, emb, repo, 16)

	if _, err := service.Ingest(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// The embedding input carries the breadcrumb so the vector encodes section context.
	want := "guide > error handling > placement of w\n\nwrap with %w"
	if len(emb.seen) != 1 || emb.seen[0] != want {
		t.Fatalf("embedded text = %#v, want %q", emb.seen, want)
	}
	// The stored document stays the raw content (clean search results + expand).
	stored := repo.bySource[doc.Path()]
	if len(stored) != 1 || stored[0].Chunk.Content() != "wrap with %w" {
		t.Fatalf("stored content = %#v", stored)
	}
}

// runeCounter is a deterministic TokenCounter: one token per rune.
type runeCounter struct{}

func (runeCounter) CountTokens(_ context.Context, texts []string) ([]int, error) {
	out := make([]int, len(texts))
	for i, t := range texts {
		out[i] = len([]rune(t))
	}
	return out, nil
}

func TestIngestEnforcesTokenBudget(t *testing.T) {
	t.Parallel()

	doc := document(t, "docs/big.md")
	big := strings.Repeat("a", 100) // 100 runes → 100 tokens; no heading path so ContextualText == content
	chunker := &fakeChunker{split: func(domain.Document) ([]domain.Chunk, error) {
		c, err := domain.NewChunk(domain.ChunkParams{SourceID: doc.Path(), Index: 0, Content: big})
		if err != nil {
			return nil, err
		}
		return []domain.Chunk{c}, nil
	}}
	repo := newFakeRepository()
	service := application.NewIngestService(
		&fakeSource{documents: []domain.Document{doc}}, chunker, &fakeEmbedder{dimension: 8}, repo, 16,
		application.WithTokenBudget(runeCounter{}, 30),
	)

	res, err := service.Ingest(context.Background())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	stored := repo.bySource[doc.Path()]
	if len(stored) < 2 {
		t.Fatalf("over-budget chunk should be split, got %d pieces", len(stored))
	}
	if res.Ingested != len(stored) {
		t.Fatalf("ingested %d != stored %d", res.Ingested, len(stored))
	}
	seen := map[int]bool{}
	for i, ec := range stored {
		if n := len([]rune(ec.Chunk.Content())); n > 30 {
			t.Fatalf("piece %d has %d runes, exceeds budget 30", i, n)
		}
		if idx := ec.Chunk.Index(); seen[idx] {
			t.Fatalf("duplicate chunk index %d after split (breaks chunk ID uniqueness)", idx)
		} else {
			seen[idx] = true
		}
	}
}

func TestIngestDedupesChunks(t *testing.T) {
	t.Parallel()

	doc := document(t, "docs/dup.md")
	mk := func(s string) domain.Chunk {
		c, err := domain.NewChunk(domain.ChunkParams{SourceID: doc.Path(), Index: 0, Content: s})
		if err != nil {
			t.Fatalf("chunk: %v", err)
		}
		return c
	}
	chunker := &fakeChunker{split: func(domain.Document) ([]domain.Chunk, error) {
		return []domain.Chunk{
			mk("the quick brown fox jumps over the lazy dog"),
			mk("the quick brown fox jumps over the lazy dog again"), // near-dup
			mk("something entirely unrelated about ships and sails"),
		}, nil
	}}
	repo := newFakeRepository()
	service := application.NewIngestService(
		&fakeSource{documents: []domain.Document{doc}}, chunker, &fakeEmbedder{dimension: 8}, repo, 16,
		application.WithDedup(0.8),
	)

	if _, err := service.Ingest(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := len(repo.bySource[doc.Path()]); got != 2 {
		t.Fatalf("dedup should drop the near-duplicate, stored %d want 2", got)
	}
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
			if !repo.prepared {
				t.Error("expected Prepare to be called")
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

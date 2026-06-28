package domain_test

import (
	"errors"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func validChunkParams() domain.ChunkParams {
	return domain.ChunkParams{
		SourceID:    "docs/go.md",
		Index:       0,
		Title:       "Naming",
		HeadingPath: "naming",
		Content:     "use short names",
	}
}

func TestNewChunk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*domain.ChunkParams)
		wantErr error
	}{
		{name: "valid", mutate: func(*domain.ChunkParams) {}, wantErr: nil},
		{name: "blank source id", mutate: func(p *domain.ChunkParams) { p.SourceID = "  " }, wantErr: domain.ErrEmptySourceID},
		{name: "blank content", mutate: func(p *domain.ChunkParams) { p.Content = "  " }, wantErr: domain.ErrEmptyContent},
		{name: "negative index", mutate: func(p *domain.ChunkParams) { p.Index = -1 }, wantErr: domain.ErrNegativeIndex},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			params := validChunkParams()
			tt.mutate(&params)
			_, err := domain.NewChunk(params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestChunkIDIdentity(t *testing.T) {
	t.Parallel()

	newChunk := func(t *testing.T, sourceID string, index int, content string) domain.Chunk {
		t.Helper()
		params := validChunkParams()
		params.SourceID = sourceID
		params.Index = index
		params.Content = content
		chunk, err := domain.NewChunk(params)
		if err != nil {
			t.Fatalf("new chunk: %v", err)
		}
		return chunk
	}

	base := newChunk(t, "docs/go.md", 0, "v1")

	t.Run("stable across content changes", func(t *testing.T) {
		t.Parallel()
		updated := newChunk(t, "docs/go.md", 0, "v2 rewritten body")
		if base.ID() != updated.ID() {
			t.Fatalf("ID changed with content: %s vs %s", base.ID(), updated.ID())
		}
	})

	t.Run("differs by index", func(t *testing.T) {
		t.Parallel()
		if base.ID() == newChunk(t, "docs/go.md", 1, "v1").ID() {
			t.Fatal("expected different IDs for different index")
		}
	})

	t.Run("differs by source", func(t *testing.T) {
		t.Parallel()
		if base.ID() == newChunk(t, "docs/style.md", 0, "v1").ID() {
			t.Fatal("expected different IDs for different source")
		}
	})
}

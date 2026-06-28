package domain_test

import (
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func TestChunkContextualText(t *testing.T) {
	t.Parallel()

	c, err := domain.NewChunk(domain.ChunkParams{
		SourceID:    "s",
		Index:       0,
		Title:       "Placement of %w in errors",
		HeadingPath: "go-style-best-practices/error-handling/placement-of-w-in-errors",
		Content:     "wrap with %w",
	})
	if err != nil {
		t.Fatalf("new chunk: %v", err)
	}

	got := c.ContextualText()
	want := "go style best practices > error handling > placement of w in errors\n\nwrap with %w"
	if got != want {
		t.Fatalf("ContextualText() = %q, want %q", got, want)
	}
}

func TestChunkContextualTextWithoutHeadingPath(t *testing.T) {
	t.Parallel()

	c, err := domain.NewChunk(domain.ChunkParams{SourceID: "s", Index: 0, Content: "preamble body"})
	if err != nil {
		t.Fatalf("new chunk: %v", err)
	}
	if got := c.ContextualText(); got != "preamble body" {
		t.Fatalf("ContextualText() = %q, want plain content", got)
	}
}

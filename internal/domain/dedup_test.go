package domain_test

import (
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func chunkWith(t *testing.T, index int, content string) domain.Chunk {
	t.Helper()
	c, err := domain.NewChunk(domain.ChunkParams{SourceID: "s", Index: index, Content: content})
	if err != nil {
		t.Fatalf("new chunk: %v", err)
	}
	return c
}

func contents(chunks []domain.Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Content()
	}
	return out
}

func TestDedupeChunksDropsNearDuplicates(t *testing.T) {
	t.Parallel()

	chunks := []domain.Chunk{
		chunkWith(t, 0, "the quick brown fox jumps over the lazy dog"),
		chunkWith(t, 1, "the quick brown fox jumps over the lazy dog today"), // near-identical
		chunkWith(t, 2, "a completely different sentence about cats and hats"),
	}

	kept := domain.DedupeChunks(chunks, 0.8)
	got := contents(kept)
	if len(kept) != 2 {
		t.Fatalf("kept %d chunks, want 2: %v", len(kept), got)
	}
	if got[0] != chunks[0].Content() || got[1] != chunks[2].Content() {
		t.Fatalf("kept the wrong chunks: %v", got)
	}
}

func TestDedupeChunksKeepsDistinctAndOverlappingWindows(t *testing.T) {
	t.Parallel()

	// Adjacent sliding windows share only a little text — they must NOT be dropped.
	chunks := []domain.Chunk{
		chunkWith(t, 0, "alpha beta gamma delta epsilon zeta eta theta"),
		chunkWith(t, 1, "theta iota kappa lambda mu nu xi omicron"),
	}
	if kept := domain.DedupeChunks(chunks, 0.8); len(kept) != 2 {
		t.Fatalf("dropped a non-duplicate window: kept %d, want 2", len(kept))
	}
}

func TestDedupeChunksDisabledOrEmpty(t *testing.T) {
	t.Parallel()

	chunks := []domain.Chunk{chunkWith(t, 0, "a a a a"), chunkWith(t, 1, "a a a a")}
	// threshold <= 0 disables dedup (identity).
	if kept := domain.DedupeChunks(chunks, 0); len(kept) != 2 {
		t.Fatalf("threshold 0 should disable dedup, kept %d", len(kept))
	}
	if kept := domain.DedupeChunks(nil, 0.8); kept != nil {
		t.Fatalf("nil input should return nil")
	}
}

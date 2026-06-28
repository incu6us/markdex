package markdown_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/incu6us/markdex/internal/domain"
	"github.com/incu6us/markdex/internal/infrastructure/markdown"
)

const fence = "```"

func newDocument(t *testing.T, content string) domain.Document {
	t.Helper()
	doc, err := domain.NewDocument("docs/go.md", content)
	if err != nil {
		t.Fatalf("new document: %v", err)
	}
	return doc
}

func split(t *testing.T, maxRunes, overlap int, content string) []domain.Chunk {
	t.Helper()
	chunks, err := markdown.NewSplitter(maxRunes, overlap).Split(newDocument(t, content))
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return chunks
}

func TestSplitOnH1(t *testing.T) {
	t.Parallel()

	content := "# Naming\nkeep names short\n\n# Errors\nwrap with percent w"
	chunks := split(t, 2000, 0, content)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Title() != "Naming" || chunks[1].Title() != "Errors" {
		t.Fatalf("titles = %q, %q", chunks[0].Title(), chunks[1].Title())
	}
	if chunks[0].HeadingPath() != "naming" || chunks[1].HeadingPath() != "errors" {
		t.Fatalf("heading paths = %q, %q", chunks[0].HeadingPath(), chunks[1].HeadingPath())
	}
	if chunks[0].Index() != 0 || chunks[1].Index() != 1 {
		t.Fatalf("indexes = %d, %d", chunks[0].Index(), chunks[1].Index())
	}
	if chunks[0].SourceID() != "docs/go.md" {
		t.Fatalf("source id = %q", chunks[0].SourceID())
	}
	if !strings.Contains(chunks[0].Content(), "keep names short") {
		t.Fatalf("first chunk missing body: %q", chunks[0].Content())
	}
}

func TestSplitNoHeadingIsSingleChunk(t *testing.T) {
	t.Parallel()

	chunks := split(t, 2000, 0, "just a paragraph with no heading at all")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Title() != "" {
		t.Fatalf("title = %q, want empty", chunks[0].Title())
	}
}

func TestSplitPreambleBeforeFirstH1(t *testing.T) {
	t.Parallel()

	content := "intro text before any heading\n\n# Section\nbody"
	chunks := split(t, 2000, 0, content)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Title() != "" {
		t.Fatalf("preamble title = %q, want empty", chunks[0].Title())
	}
	if !strings.Contains(chunks[0].Content(), "intro text") {
		t.Fatalf("preamble content = %q", chunks[0].Content())
	}
	if chunks[1].Title() != "Section" {
		t.Fatalf("second title = %q, want Section", chunks[1].Title())
	}
}

func TestSplitIgnoresHeadingsInsideCodeFence(t *testing.T) {
	t.Parallel()

	content := "# Real\nbefore\n" + fence + "go\n# not a heading\nfmt.Println()\n" + fence + "\nafter"
	chunks := split(t, 2000, 0, content)

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (fence must not split)", len(chunks))
	}
	if !strings.Contains(chunks[0].Content(), "# not a heading") {
		t.Fatalf("fenced content lost: %q", chunks[0].Content())
	}
}

func TestSplitRecursesIntoH2WhenOversized(t *testing.T) {
	t.Parallel()

	const maxRunes = 70
	body1 := strings.Repeat("a", 50)
	body2 := strings.Repeat("b", 50)
	content := "# Topic\n## First\n" + body1 + "\n## Second\n" + body2
	chunks := split(t, maxRunes, 0, content)

	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(chunks))
	}
	for _, c := range chunks {
		if c.Title() != "Topic" {
			t.Fatalf("sub-chunk title = %q, want Topic", c.Title())
		}
		if utf8.RuneCountInString(c.Content()) > maxRunes {
			t.Fatalf("chunk exceeds maxRunes: %d > %d", utf8.RuneCountInString(c.Content()), maxRunes)
		}
	}
	if !hasHeadingPathPrefix(chunks, "topic/first") || !hasHeadingPathPrefix(chunks, "topic/second") {
		t.Fatalf("expected h1/h2 heading paths, got %v", headingPaths(chunks))
	}
}

func TestSplitWindowsOversizedProseWithOverlap(t *testing.T) {
	t.Parallel()

	const maxRunes = 80
	const overlap = 20
	content := "# Prose\n" + strings.Repeat("x", 240)
	chunks := split(t, maxRunes, overlap, content)

	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(chunks))
	}
	total := 0
	for _, c := range chunks {
		runes := utf8.RuneCountInString(c.Content())
		if runes > maxRunes {
			t.Fatalf("chunk exceeds maxRunes: %d > %d", runes, maxRunes)
		}
		total += runes
	}
	if total <= 240 {
		t.Fatalf("expected overlap to inflate total runes beyond 240, got %d", total)
	}
}

func TestSplitInvariantNoChunkExceedsMaxRunes(t *testing.T) {
	t.Parallel()

	const maxRunes = 100
	content := "preamble " + strings.Repeat("p", 300) +
		"\n# One\n" + strings.Repeat("o", 250) +
		"\n## Nested\n" + strings.Repeat("n", 250) +
		"\n# Two\nshort"
	chunks := split(t, maxRunes, 15, content)

	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	for i, c := range chunks {
		if utf8.RuneCountInString(c.Content()) > maxRunes {
			t.Fatalf("chunk %d exceeds maxRunes", i)
		}
		if strings.TrimSpace(c.Content()) == "" {
			t.Fatalf("chunk %d is blank", i)
		}
		if c.Index() != i {
			t.Fatalf("chunk %d has index %d", i, c.Index())
		}
	}
}

func headingPaths(chunks []domain.Chunk) []string {
	paths := make([]string, len(chunks))
	for i, c := range chunks {
		paths[i] = c.HeadingPath()
	}
	return paths
}

func hasHeadingPathPrefix(chunks []domain.Chunk, prefix string) bool {
	for _, c := range chunks {
		if strings.HasPrefix(c.HeadingPath(), prefix) {
			return true
		}
	}
	return false
}

package domain_test

import (
	"errors"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func TestNewDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		content string
		wantErr error
	}{
		{name: "valid", path: "docs/a.md", content: "hello", wantErr: nil},
		{name: "blank path", path: "  ", content: "hello", wantErr: domain.ErrEmptyPath},
		{name: "blank content", path: "docs/a.md", content: "  ", wantErr: domain.ErrEmptyContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewDocument(tt.path, tt.content)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDocumentIDIsDeterministic(t *testing.T) {
	t.Parallel()

	first, err := domain.NewDocument("docs/a.md", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := domain.NewDocument("docs/a.md", "v2 updated content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.ID() != second.ID() {
		t.Fatalf("expected same ID for same path, got %s and %s", first.ID(), second.ID())
	}

	other, err := domain.NewDocument("docs/b.md", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.ID() == other.ID() {
		t.Fatalf("expected different IDs for different paths")
	}
}

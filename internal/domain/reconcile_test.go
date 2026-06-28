package domain_test

import (
	"reflect"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func TestSourcesToPrune(t *testing.T) {
	t.Parallel()

	existing := []string{
		"https://raw.example/o/r/main/a.md",
		"https://raw.example/o/r/main/b.md",
		"https://raw.example/o/r/main/gone.md", // in scope, not kept → prune
		"upload://notes.md",                    // out of scope → keep
	}
	kept := []string{
		"https://raw.example/o/r/main/a.md",
		"https://raw.example/o/r/main/b.md",
	}
	scope := "https://raw.example/o/r/"

	got := domain.SourcesToPrune(existing, kept, scope)
	want := []string{"https://raw.example/o/r/main/gone.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prune = %v, want %v", got, want)
	}
}

func TestSourcesToPruneEmptyScopeIsNoop(t *testing.T) {
	t.Parallel()
	// No scope → never prune (safety: don't wipe a whole multi-source collection).
	if got := domain.SourcesToPrune([]string{"a", "b"}, []string{"a"}, ""); got != nil {
		t.Fatalf("empty scope should prune nothing, got %v", got)
	}
}

func TestSourcesToPruneNothingStale(t *testing.T) {
	t.Parallel()
	existing := []string{"s/a.md", "s/b.md"}
	if got := domain.SourcesToPrune(existing, existing, "s/"); got != nil {
		t.Fatalf("nothing stale should prune nothing, got %v", got)
	}
}

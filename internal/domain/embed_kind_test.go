package domain_test

import (
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func TestEmbedKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind domain.EmbedKind
		want string
	}{
		{name: "document", kind: domain.DocumentKind, want: "document"},
		{name: "query", kind: domain.QueryKind, want: "query"},
		{name: "zero value is document", kind: domain.EmbedKind(0), want: "document"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.kind.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

package domain_test

import (
	"errors"
	"testing"

	"github.com/incu6us/markdex/internal/domain"
)

func TestNewSparseEmbedding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		indices []uint32
		values  []float32
		wantErr error
	}{
		{name: "valid", indices: []uint32{1, 42, 100}, values: []float32{0.5, 0.2, 0.1}, wantErr: nil},
		{name: "empty allowed", indices: nil, values: nil, wantErr: nil},
		{name: "length mismatch", indices: []uint32{1, 2}, values: []float32{0.5}, wantErr: domain.ErrSparseLengthMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewSparseEmbedding(tt.indices, tt.values)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got.Len() != len(tt.indices) {
				t.Fatalf("Len() = %d, want %d", got.Len(), len(tt.indices))
			}
		})
	}
}

func TestSparseEmbeddingAccessors(t *testing.T) {
	t.Parallel()

	s, err := domain.NewSparseEmbedding([]uint32{7, 9}, []float32{0.3, 0.4})
	if err != nil {
		t.Fatalf("new sparse: %v", err)
	}
	if s.IsEmpty() {
		t.Fatal("expected non-empty")
	}
	if len(s.Indices()) != 2 || s.Indices()[0] != 7 {
		t.Fatalf("indices = %v", s.Indices())
	}
	if len(s.Values()) != 2 || s.Values()[1] != 0.4 {
		t.Fatalf("values = %v", s.Values())
	}

	empty, _ := domain.NewSparseEmbedding(nil, nil)
	if !empty.IsEmpty() {
		t.Fatal("expected empty")
	}
}

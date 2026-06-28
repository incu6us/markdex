package domain

import "errors"

var ErrSparseLengthMismatch = errors.New("sparse embedding indices and values must have equal length")

type SparseEmbedding struct {
	indices []uint32
	values  []float32
}

func NewSparseEmbedding(indices []uint32, values []float32) (SparseEmbedding, error) {
	if len(indices) != len(values) {
		return SparseEmbedding{}, ErrSparseLengthMismatch
	}
	return SparseEmbedding{indices: indices, values: values}, nil
}

func (s SparseEmbedding) Indices() []uint32 { return s.indices }

func (s SparseEmbedding) Values() []float32 { return s.values }

func (s SparseEmbedding) Len() int { return len(s.indices) }

func (s SparseEmbedding) IsEmpty() bool { return len(s.indices) == 0 }

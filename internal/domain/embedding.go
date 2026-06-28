package domain

import "errors"

var ErrEmptyVector = errors.New("embedding vector must not be empty")

type Embedding struct {
	vector []float32
}

func NewEmbedding(vector []float32) (Embedding, error) {
	if len(vector) == 0 {
		return Embedding{}, ErrEmptyVector
	}
	return Embedding{vector: vector}, nil
}

func (e Embedding) Vector() []float32 { return e.vector }

func (e Embedding) Dimension() int { return len(e.vector) }

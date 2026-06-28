package domain

import "context"

type Ranked struct {
	Index int
	Score float32
}

type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string, topK int) ([]Ranked, error)
}

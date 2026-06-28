package domain

import "context"

type DocumentSource interface {
	Load(ctx context.Context) ([]Document, error)
}

type Chunker interface {
	Split(doc Document) ([]Chunk, error)
}

type Embedder interface {
	Embed(ctx context.Context, texts []string, kind EmbedKind) ([]Vectors, error)
}

type VectorRepository interface {
	Prepare(ctx context.Context) error
	Replace(ctx context.Context, sourceID string, chunks []EmbeddedChunk) error
	Search(ctx context.Context, query Vectors, topN int, filter Filter) ([]SearchHit, error)
}

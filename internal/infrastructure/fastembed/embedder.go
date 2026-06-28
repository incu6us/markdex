package fastembed

import (
	"context"
	"fmt"
	"math"
	"strings"

	lib "github.com/anush008/fastembed-go"

	"github.com/incu6us/markdex/internal/domain"
)

const (
	modelName    = "BAAI/bge-small-en-v1.5"
	libMaxLength = math.MaxInt32
	minCharLimit = 256
)

type Embedder struct {
	model     *lib.FlagEmbedding
	dimension int
	maxChars  int
}

func New(cacheDir string, maxChars int) (*Embedder, error) {
	if maxChars < minCharLimit {
		maxChars = minCharLimit
	}
	model, err := lib.NewFlagEmbedding(&lib.InitOptions{
		Model:     lib.BGESmallENV15,
		CacheDir:  cacheDir,
		MaxLength: libMaxLength,
	})
	if err != nil {
		return nil, fmt.Errorf("init fastembed model: %w", err)
	}

	probe, err := embedOnce(model, "dimension probe")
	if err != nil {
		model.Destroy()
		return nil, fmt.Errorf("probe embedding dimension: %w", err)
	}

	return &Embedder{model: model, dimension: len(probe), maxChars: maxChars}, nil
}

func (e *Embedder) Embed(ctx context.Context, contents []string) ([]domain.Embedding, error) {
	embeddings := make([]domain.Embedding, len(contents))
	for i, content := range contents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		vector, err := e.embedFitting(content)
		if err != nil {
			return nil, fmt.Errorf("embed document %d: %w", i, err)
		}

		embedding, embErr := domain.NewEmbedding(vector)
		if embErr != nil {
			return nil, embErr
		}
		embeddings[i] = embedding
	}
	return embeddings, nil
}

// embedFitting embeds content, shrinking it until it fits the model's 512-token
// window. fastembed runs with an effectively unlimited MaxLength so its own
// truncation never executes (sugarme/tokenizer's LongestFirst truncation dereferences
// a nil pair-encoding and panics on single sequences); instead we truncate by runes
// here and halve the budget whenever onnxruntime reports a sequence overflow.
func (e *Embedder) embedFitting(content string) ([]float32, error) {
	runes := []rune(content)
	limit := e.maxChars
	for {
		text := content
		if len(runes) > limit {
			text = string(runes[:limit])
		}

		vector, err := embedOnce(e.model, text)
		if err == nil {
			return vector, nil
		}
		if !isSequenceOverflow(err) || limit <= minCharLimit {
			return nil, err
		}
		limit /= 2
	}
}

// embedOnce embeds exactly one text. We never hand fastembed more than one document
// at a time: it spawns a goroutine per batch element that shares a single
// non-concurrency-safe tokenizer, which intermittently panics with a nil Encoding.
func embedOnce(model *lib.FlagEmbedding, content string) ([]float32, error) {
	vectors, err := model.Embed([]string{content}, 1)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("empty vector")
	}
	return vectors[0], nil
}

func isSequenceOverflow(err error) bool {
	return strings.Contains(err.Error(), "broadcast")
}

func (e *Embedder) Dimension() int { return e.dimension }

func (e *Embedder) VectorName() string {
	name := modelName
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return "fast-" + strings.ToLower(name)
}

func (e *Embedder) Close() error {
	e.model.Destroy()
	return nil
}

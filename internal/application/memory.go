package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/incu6us/markdex/internal/domain"
)

// memoryType is the metadata.type value tagging every memory point. It is also the
// supersede-probe filter, so a memory write can only ever replace another memory —
// never a curated document, even in a shared collection (the clobber guard).
const memoryType = "memory"

// probeTopK is how many existing memories the supersede probe retrieves; only the
// best lexical/semantic match among them is considered for replacement.
const probeTopK = 3

const (
	// defaultSupersedeThreshold is conservative: it errs toward appending, since a
	// missed dedup is harmless but a wrong supersede loses information. Calibrated to
	// 0.97 (see docs/memory_plan.md) — the lowest cutoff with zero wrong supersedes
	// on a labeled same/different memory-pair set.
	defaultSupersedeThreshold = 0.97
	// defaultDedupThreshold mirrors ingest dedup — near-identical restatements.
	defaultDedupThreshold = 0.9
)

// MemorySearcher retrieves existing candidates from a named collection. It is the
// same hybrid+rerank retrieval used by /api/search, parameterized by collection.
type MemorySearcher interface {
	Search(ctx context.Context, collection, query string, topK int, filter domain.Filter, expand bool) ([]domain.SearchHit, error)
}

// MemoryStore is the collection-aware write side: ensure the collection exists,
// upsert/replace a memory by its source_id, and delete by source_id (forget).
type MemoryStore interface {
	Prepare(ctx context.Context, collection string) error
	Replace(ctx context.Context, collection, sourceID string, chunks []domain.EmbeddedChunk) error
	DeleteSources(ctx context.Context, collection string, ids []string) error
}

// RememberParams are the inputs to Remember.
type RememberParams struct {
	Collection string
	Text       string
	Author     string
	Namespace  string
	Tags       string
	// SupersedeThreshold overrides the service's configured semantic cutoff for this
	// one write (nil = use the default). Lower = merge more aggressively.
	SupersedeThreshold *float64
}

// RememberResult reports what Remember did.
type RememberResult struct {
	SourceID   string
	Superseded bool
	Version    int
}

type MemoryOption func(*MemoryService)

// WithClock injects the time source (for deterministic tests).
func WithClock(now func() time.Time) MemoryOption {
	return func(s *MemoryService) {
		if now != nil {
			s.now = now
		}
	}
}

// WithIDGenerator injects the memory id source (for deterministic tests).
func WithIDGenerator(gen func() string) MemoryOption {
	return func(s *MemoryService) {
		if gen != nil {
			s.newID = gen
		}
	}
}

// WithSupersedeThreshold sets the rerank-score cutoff above which a candidate
// memory is replaced rather than appended.
func WithSupersedeThreshold(threshold float64) MemoryOption {
	return func(s *MemoryService) { s.supersedeThreshold = threshold }
}

// WithDedupThreshold sets the word-shingle Jaccard cutoff for the lexical
// supersede pre-gate.
func WithDedupThreshold(threshold float64) MemoryOption {
	return func(s *MemoryService) { s.dedupThreshold = threshold }
}

// MemoryService implements agent write-back: it stores a fact as a single-chunk
// point, superseding a near-identical existing memory in place (semantic + lexical)
// or appending a fresh one. Retrieval, embedding, and storage are all reused.
type MemoryService struct {
	embedder           domain.Embedder
	searcher           MemorySearcher
	store              MemoryStore
	now                func() time.Time
	newID              func() string
	supersedeThreshold float64
	dedupThreshold     float64
}

func NewMemoryService(embedder domain.Embedder, searcher MemorySearcher, store MemoryStore, opts ...MemoryOption) *MemoryService {
	s := &MemoryService{
		embedder:           embedder,
		searcher:           searcher,
		store:              store,
		now:                time.Now,
		newID:              func() string { return domain.NewID() },
		supersedeThreshold: defaultSupersedeThreshold,
		dedupThreshold:     defaultDedupThreshold,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *MemoryService) Remember(ctx context.Context, p RememberParams) (RememberResult, error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return RememberResult{}, errors.New("memory text must not be empty")
	}
	collection := strings.TrimSpace(p.Collection)
	if collection == "" {
		return RememberResult{}, errors.New("collection must not be empty")
	}

	if err := s.store.Prepare(ctx, collection); err != nil {
		return RememberResult{}, err
	}

	// Supersede probe — memories only (the clobber guard).
	candidates, err := s.searcher.Search(ctx, collection, text, probeTopK,
		domain.Filter{Match: map[string]string{"type": memoryType}}, false)
	if err != nil {
		return RememberResult{}, err
	}

	threshold := s.supersedeThreshold
	if p.SupersedeThreshold != nil {
		threshold = *p.SupersedeThreshold
	}

	now := s.now().UTC().Format(time.RFC3339)
	version := 1
	// Slash-free scheme so the source_id is a single, route-safe URL path segment
	// for DELETE /api/memories/{id}.
	sourceID := "memory:" + s.newID()
	supersededID := ""
	createdAt := now

	if match := s.bestSupersede(text, candidates, threshold); match != nil {
		sourceID = match.Metadata["source_id"]
		supersededID = sourceID
		version = parseVersion(match.Metadata["version"]) + 1
		if c := match.Metadata["created_at"]; c != "" {
			createdAt = c // preserve the original creation time across supersedes
		}
	}

	meta := map[string]string{
		"type":       memoryType,
		"created_at": createdAt,
		"updated_at": now,
		"version":    strconv.Itoa(version),
	}
	putIfSet(meta, "author", p.Author)
	putIfSet(meta, "namespace", p.Namespace)
	putIfSet(meta, "tags", p.Tags)
	putIfSet(meta, "supersedes", supersededID)

	vectors, err := s.embedder.Embed(ctx, []string{text}, domain.DocumentKind)
	if err != nil {
		return RememberResult{}, err
	}
	if len(vectors) != 1 {
		return RememberResult{}, errors.New("embedder returned no vector for memory")
	}

	chunk, err := domain.NewChunk(domain.ChunkParams{
		SourceID: sourceID,
		Index:    0,
		Content:  text,
		Metadata: meta,
	})
	if err != nil {
		return RememberResult{}, err
	}

	if err := s.store.Replace(ctx, collection, sourceID, []domain.EmbeddedChunk{{Chunk: chunk, Vectors: vectors[0]}}); err != nil {
		return RememberResult{}, err
	}
	return RememberResult{SourceID: sourceID, Superseded: supersededID != "", Version: version}, nil
}

func (s *MemoryService) Forget(ctx context.Context, collection, id string) error {
	collection = strings.TrimSpace(collection)
	id = strings.TrimSpace(id)
	if collection == "" || id == "" {
		return errors.New("collection and memory id are required")
	}
	return s.store.DeleteSources(ctx, collection, []string{id})
}

// bestSupersede returns the first candidate (in rank order) that the new text
// should replace — a lexical near-duplicate (cheap pre-gate) or a candidate whose
// rerank score clears the semantic threshold — or nil to append. Candidates with
// no source_id are skipped (cannot be replaced in place).
func (s *MemoryService) bestSupersede(text string, candidates []domain.SearchHit, supersedeThreshold float64) *domain.SearchHit {
	for i := range candidates {
		c := candidates[i]
		if c.Metadata["source_id"] == "" {
			continue
		}
		if domain.ShingleSimilarity(text, c.Document) >= s.dedupThreshold {
			return &c
		}
		if float64(c.Score) >= supersedeThreshold {
			return &c
		}
	}
	return nil
}

func putIfSet(m map[string]string, key, value string) {
	if strings.TrimSpace(value) != "" {
		m[key] = value
	}
}

func parseVersion(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < 1 {
		return 1
	}
	return v
}

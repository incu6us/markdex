package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/incu6us/markdex/internal/application"
	"github.com/incu6us/markdex/internal/domain"
)

type fakeMemorySearcher struct {
	hits          []domain.SearchHit
	err           error
	gotCollection string
	gotQuery      string
	gotFilter     domain.Filter
}

func (f *fakeMemorySearcher) Search(_ context.Context, collection, query string, _ int, filter domain.Filter, _ bool) ([]domain.SearchHit, error) {
	f.gotCollection, f.gotQuery, f.gotFilter = collection, query, filter
	return f.hits, f.err
}

type fakeMemoryStore struct {
	prepared      []string
	replaced      map[string][]domain.EmbeddedChunk
	deleted       []string
	gotCollection string
}

func newFakeMemoryStore() *fakeMemoryStore {
	return &fakeMemoryStore{replaced: map[string][]domain.EmbeddedChunk{}}
}

func (s *fakeMemoryStore) Prepare(_ context.Context, collection string) error {
	s.prepared = append(s.prepared, collection)
	return nil
}

func (s *fakeMemoryStore) Replace(_ context.Context, collection, sourceID string, chunks []domain.EmbeddedChunk) error {
	s.gotCollection = collection
	s.replaced[sourceID] = chunks
	return nil
}

func (s *fakeMemoryStore) DeleteSources(_ context.Context, collection string, ids []string) error {
	s.gotCollection = collection
	s.deleted = append(s.deleted, ids...)
	return nil
}

func newMemoryService(t *testing.T, searcher application.MemorySearcher, store application.MemoryStore, opts ...application.MemoryOption) *application.MemoryService {
	t.Helper()
	fixed := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	base := []application.MemoryOption{
		application.WithClock(func() time.Time { return fixed }),
		application.WithIDGenerator(func() string { return "fixed-id" }),
	}
	return application.NewMemoryService(&fakeEmbedder{dimension: 8}, searcher, store, append(base, opts...)...)
}

func TestMemoryRememberAppendsWhenNoCandidate(t *testing.T) {
	t.Parallel()

	searcher := &fakeMemorySearcher{} // no hits
	store := newFakeMemoryStore()
	svc := newMemoryService(t, searcher, store)

	res, err := svc.Remember(context.Background(), application.RememberParams{
		Collection: "team-memory", Text: "Acme is on legacy billing", Author: "agent:claude-code",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}

	if res.SourceID != "memory:fixed-id" {
		t.Fatalf("source id = %q, want memory:fixed-id", res.SourceID)
	}
	if res.Superseded {
		t.Fatal("first write must not supersede")
	}
	if res.Version != 1 {
		t.Fatalf("version = %d, want 1", res.Version)
	}
	if len(store.prepared) != 1 || store.prepared[0] != "team-memory" {
		t.Fatalf("prepared = %v, want [team-memory]", store.prepared)
	}
	chunks := store.replaced["memory:fixed-id"]
	if len(chunks) != 1 {
		t.Fatalf("stored %d chunks, want 1", len(chunks))
	}
	md := chunks[0].Chunk.Metadata()
	if md["type"] != "memory" || md["author"] != "agent:claude-code" {
		t.Fatalf("metadata = %v", md)
	}
	if md["created_at"] != "2026-06-29T12:00:00Z" || md["version"] != "1" {
		t.Fatalf("metadata timestamps/version = %v", md)
	}
}

func TestMemoryRememberProbesMemoriesOnly(t *testing.T) {
	t.Parallel()

	searcher := &fakeMemorySearcher{}
	svc := newMemoryService(t, searcher, newFakeMemoryStore())

	if _, err := svc.Remember(context.Background(), application.RememberParams{Collection: "docs", Text: "a fact"}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	// The clobber guard: the supersede probe must filter to memories only, so a
	// memory written into a doc collection can never replace (delete) a document.
	if searcher.gotFilter.Match["type"] != "memory" {
		t.Fatalf("probe filter = %v, want type=memory", searcher.gotFilter.Match)
	}
	if searcher.gotCollection != "docs" {
		t.Fatalf("probe collection = %q", searcher.gotCollection)
	}
}

func TestMemoryRememberSupersedesOnSemanticHit(t *testing.T) {
	t.Parallel()

	searcher := &fakeMemorySearcher{hits: []domain.SearchHit{
		{ID: "p1", Score: 0.95, Document: "Acme uses the old billing", Metadata: map[string]string{
			"source_id": "memory://old", "type": "memory", "version": "2", "created_at": "2026-01-01T00:00:00Z",
		}},
	}}
	store := newFakeMemoryStore()
	svc := newMemoryService(t, searcher, store, application.WithSupersedeThreshold(0.9))

	res, err := svc.Remember(context.Background(), application.RememberParams{
		Collection: "team-memory", Text: "Acme is billed on the legacy plan",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !res.Superseded || res.SourceID != "memory://old" {
		t.Fatalf("res = %+v, want supersede of memory://old", res)
	}
	if res.Version != 3 {
		t.Fatalf("version = %d, want 3 (bumped from 2)", res.Version)
	}
	chunks := store.replaced["memory://old"]
	if len(chunks) != 1 {
		t.Fatalf("expected in-place replace under memory://old, got %v", store.replaced)
	}
	md := chunks[0].Chunk.Metadata()
	if md["supersedes"] != "memory://old" {
		t.Fatalf("supersedes = %q", md["supersedes"])
	}
	if md["created_at"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("created_at = %q, want the original preserved", md["created_at"])
	}
}

func TestMemoryRememberThresholdBoundary(t *testing.T) {
	t.Parallel()

	// A candidate whose rerank score is just below the threshold and whose text is
	// lexically unrelated must NOT supersede — it appends instead.
	searcher := &fakeMemorySearcher{hits: []domain.SearchHit{
		{ID: "p1", Score: 0.89, Document: "completely different topic about deployment", Metadata: map[string]string{
			"source_id": "memory://old", "type": "memory",
		}},
	}}
	store := newFakeMemoryStore()
	svc := newMemoryService(t, searcher, store,
		application.WithSupersedeThreshold(0.9), application.WithDedupThreshold(0.9))

	res, err := svc.Remember(context.Background(), application.RememberParams{Collection: "m", Text: "a brand new unrelated fact"})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if res.Superseded || res.SourceID != "memory:fixed-id" {
		t.Fatalf("res = %+v, want append (below threshold)", res)
	}
}

func TestMemoryRememberLexicalPreGate(t *testing.T) {
	t.Parallel()

	// Rerank score is below the semantic threshold, but the text is a near-identical
	// restatement, so the lexical Jaccard pre-gate triggers a supersede.
	text := "the quick brown fox jumps over the lazy dog"
	searcher := &fakeMemorySearcher{hits: []domain.SearchHit{
		{ID: "p1", Score: 0.10, Document: "the quick brown fox jumps over the lazy dog today", Metadata: map[string]string{
			"source_id": "memory://old", "type": "memory",
		}},
	}}
	store := newFakeMemoryStore()
	svc := newMemoryService(t, searcher, store,
		application.WithSupersedeThreshold(0.9), application.WithDedupThreshold(0.8))

	res, err := svc.Remember(context.Background(), application.RememberParams{Collection: "m", Text: text})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !res.Superseded || res.SourceID != "memory://old" {
		t.Fatalf("res = %+v, want supersede via lexical pre-gate", res)
	}
}

func TestMemoryRememberPerRequestThreshold(t *testing.T) {
	t.Parallel()

	// A candidate scoring 0.92, lexically unrelated to the new text. With the default
	// (0.97) it appends; a per-request override of 0.90 makes it supersede.
	hits := []domain.SearchHit{
		{ID: "p1", Score: 0.92, Document: "totally unrelated wording about deployment cadence", Metadata: map[string]string{
			"source_id": "memory://old", "type": "memory",
		}},
	}
	threshold := 0.90

	t.Run("override lowers the bar → supersede", func(t *testing.T) {
		t.Parallel()
		store := newFakeMemoryStore()
		svc := newMemoryService(t, &fakeMemorySearcher{hits: hits}, store) // default 0.97
		res, err := svc.Remember(context.Background(), application.RememberParams{
			Collection: "m", Text: "a fresh fact", SupersedeThreshold: &threshold,
		})
		if err != nil {
			t.Fatalf("remember: %v", err)
		}
		if !res.Superseded || res.SourceID != "memory://old" {
			t.Fatalf("res = %+v, want supersede via per-request threshold", res)
		}
	})

	t.Run("no override uses the default → append", func(t *testing.T) {
		t.Parallel()
		store := newFakeMemoryStore()
		svc := newMemoryService(t, &fakeMemorySearcher{hits: hits}, store) // default 0.97
		res, err := svc.Remember(context.Background(), application.RememberParams{
			Collection: "m", Text: "a fresh fact",
		})
		if err != nil {
			t.Fatalf("remember: %v", err)
		}
		if res.Superseded {
			t.Fatalf("res = %+v, want append at default 0.97 (candidate scored 0.92)", res)
		}
	})
}

func TestMemoryRememberValidatesText(t *testing.T) {
	t.Parallel()

	svc := newMemoryService(t, &fakeMemorySearcher{}, newFakeMemoryStore())
	if _, err := svc.Remember(context.Background(), application.RememberParams{Collection: "m", Text: "   "}); err == nil {
		t.Fatal("expected error for blank text")
	}
	if _, err := svc.Remember(context.Background(), application.RememberParams{Collection: " ", Text: "fact"}); err == nil {
		t.Fatal("expected error for blank collection")
	}
}

func TestMemoryForget(t *testing.T) {
	t.Parallel()

	store := newFakeMemoryStore()
	svc := newMemoryService(t, &fakeMemorySearcher{}, store)

	if err := svc.Forget(context.Background(), "team-memory", "memory://gone"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if store.gotCollection != "team-memory" || len(store.deleted) != 1 || store.deleted[0] != "memory://gone" {
		t.Fatalf("deleted = %v in %q", store.deleted, store.gotCollection)
	}
}

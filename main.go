package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/incu6us/markdex/internal/application"
	"github.com/incu6us/markdex/internal/domain"
	"github.com/incu6us/markdex/internal/infrastructure/embedderclient"
	"github.com/incu6us/markdex/internal/infrastructure/github"
	"github.com/incu6us/markdex/internal/infrastructure/httpapi"
	"github.com/incu6us/markdex/internal/infrastructure/markdown"
	"github.com/incu6us/markdex/internal/infrastructure/qdrant"
)

const (
	embedderMaxChars = 2000
	defaultOverlap   = 200
	// embedderMaxTokens mirrors the embedder's MAX_LENGTH (BGE-M3, 8192): chunks are
	// re-split if their embedded text exceeds it, so nothing is silently truncated.
	embedderMaxTokens = 8192
	// dedupThreshold drops chunks that are >=90% shingle-identical to an earlier one.
	dedupThreshold  = 0.9
	defaultPoolSize = 24
	// memorySupersedeThreshold is the rerank-score cutoff above which a new memory
	// replaces a near-identical existing one (the sidecar reranker emits ~0..1
	// scores). Calibrated to 0.97: on a labeled same/different memory-pair set this
	// was the lowest cutoff with zero wrong supersedes (see docs/memory_plan.md).
	// Tune per corpus; raise it to be even more conservative.
	memorySupersedeThreshold = 0.97
)

//go:embed all:web/dist
var webDist embed.FS

func main() {
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", "http://localhost:6333"), "Qdrant REST base URL")
	embedderURL := flag.String("embedder", envOr("EMBEDDER_URL", "http://localhost:8000"), "embedder sidecar base URL")
	addr := flag.String("addr", ":4334", "HTTP listen address")
	poolSize := flag.Int("pool", defaultPoolSize, "rerank candidate pool size (lower = faster search, higher = better recall)")
	supersedeThreshold := flag.Float64("supersede-threshold", memorySupersedeThreshold, "rerank-score cutoff above which a new memory replaces a near-identical one (higher = more conservative)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	embedder := embedderclient.New(*embedderURL)
	info, err := waitForEmbedder(ctx, embedder, logger)
	if err != nil {
		log.Fatalf("embedder sidecar not ready: %v", err)
	}
	logger.Info("embedder ready", "model", info.EmbedModel, "dimension", info.DenseDim)

	schema := domain.CollectionSchema{
		DenseDimension: info.DenseDim,
		DenseVector:    info.DenseName,
		SparseVector:   info.SparseName,
	}
	model := httpapi.ModelInfo{Dimension: info.DenseDim, VectorName: info.DenseName}

	if err := serveAPI(ctx, *addr, *qdrantURL, os.Getenv("QDRANT_API_KEY"), embedder, schema, model, *poolSize, *supersedeThreshold, logger); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func waitForEmbedder(ctx context.Context, embedder *embedderclient.Client, logger *slog.Logger) (embedderclient.Info, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if err := embedder.Ready(ctx); err == nil {
			return embedder.Info(ctx)
		} else if time.Now().After(deadline) {
			return embedderclient.Info{}, err
		}
		logger.Info("waiting for embedder sidecar to load models...")
		select {
		case <-ctx.Done():
			return embedderclient.Info{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func serveAPI(
	ctx context.Context,
	addr, qdrantURL, apiKey string,
	embedder *embedderclient.Client,
	schema domain.CollectionSchema,
	model httpapi.ModelInfo,
	poolSize int,
	supersedeThreshold float64,
	logger *slog.Logger,
) error {
	ingester := &chunkIngester{embedder: embedder, counter: embedder, fetcher: github.NewFetcher(), logger: logger, qdrantURL: qdrantURL, apiKey: apiKey, schema: schema}
	manager := httpapi.NewJobManager(ingester, logger)
	manager.Start()
	defer manager.Stop()

	// One searcher instance plays two roles: the /api/search handler's Searcher and
	// the memory write-path's supersede-probe (its signature satisfies both ports).
	searcher := &chunkSearcher{embedder: embedder, reranker: embedder, qdrantURL: qdrantURL, apiKey: apiKey, schema: schema, poolSize: poolSize, logger: logger}
	memoryService := application.NewMemoryService(embedder, searcher,
		&memoryStore{qdrantURL: qdrantURL, apiKey: apiKey, schema: schema},
		application.WithSupersedeThreshold(supersedeThreshold),
		application.WithDedupThreshold(dedupThreshold),
	)

	server := httpapi.NewServer(httpapi.Config{
		Chunker:      markdown.NewSplitter(embedderMaxChars, defaultOverlap),
		Fetcher:      github.NewFetcher(),
		RepoLister:   github.NewRepoLister(),
		Lister:       &collectionLister{repo: qdrant.NewRepository(qdrantURL, apiKey, "", domain.CollectionSchema{})},
		Creator:      &collectionCreator{qdrantURL: qdrantURL, apiKey: apiKey, schema: schema},
		Deleter:      &collectionDeleter{qdrantURL: qdrantURL, apiKey: apiKey},
		Headings:     &headingsProvider{qdrantURL: qdrantURL, apiKey: apiKey},
		Searcher:     searcher,
		Memorizer:    &memorizer{svc: memoryService},
		MemoryLister: &memoryLister{qdrantURL: qdrantURL, apiKey: apiKey},
		Jobs:         manager,
		Model:        model,
		UI:           embeddedUI(logger),
		Logger:       logger,
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("http api listening", "addr", addr)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func embeddedUI(logger *slog.Logger) fs.FS {
	dist, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		logger.Warn("embedded web UI not built; serving API only (run `make ui-build`)")
		return nil
	}
	return dist
}

type chunkIngester struct {
	embedder  domain.Embedder
	counter   domain.TokenCounter
	fetcher   *github.Fetcher
	logger    *slog.Logger
	qdrantURL string
	apiKey    string
	schema    domain.CollectionSchema
}

// repoDocumentSource fetches a set of raw .md URLs and turns each into a Document,
// skipping (and logging) any that fail so one bad file doesn't abort the whole repo.
type repoDocumentSource struct {
	urls    []string
	fetcher *github.Fetcher
	logger  *slog.Logger
}

func (s *repoDocumentSource) Load(ctx context.Context) ([]domain.Document, error) {
	docs := make([]domain.Document, 0, len(s.urls))
	for _, u := range s.urls {
		content, err := s.fetcher.Fetch(ctx, u)
		if err != nil {
			s.logger.Warn("repo ingest: skipping file", "url", u, "err", err)
			continue
		}
		doc, err := domain.NewDocument(u, content)
		if err != nil {
			s.logger.Warn("repo ingest: skipping file", "url", u, "err", err)
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, errors.New("no documents could be fetched from the repo")
	}
	return docs, nil
}

func (c *chunkIngester) Ingest(ctx context.Context, spec httpapi.IngestSpec, report func(processed, total int)) (int, error) {
	maxChars := spec.MaxChars
	if maxChars < 1 {
		maxChars = embedderMaxChars
	}
	overlap := spec.Overlap
	if overlap < 1 {
		overlap = defaultOverlap
	}

	var source domain.DocumentSource
	switch {
	case len(spec.Files) > 0:
		docs := make([]domain.Document, 0, len(spec.Files))
		for _, f := range spec.Files {
			doc, err := domain.NewDocument(f.Name, f.Content)
			if err != nil {
				c.logger.Warn("folder ingest: skipping file", "name", f.Name, "err", err)
				continue
			}
			docs = append(docs, doc)
		}
		if len(docs) == 0 {
			return 0, errors.New("no non-empty .md files to ingest")
		}
		source = documentSource{documents: docs}
	case len(spec.URLs) > 0:
		source = &repoDocumentSource{urls: spec.URLs, fetcher: c.fetcher, logger: c.logger}
	default:
		document, err := domain.NewDocument(spec.Name, spec.Content)
		if err != nil {
			return 0, err
		}
		source = documentSource{documents: []domain.Document{document}}
	}
	chunker := markdown.NewSplitter(maxChars, overlap)
	repo := qdrant.NewRepository(c.qdrantURL, c.apiKey, spec.Collection, c.schema)
	service := application.NewIngestService(source, chunker, c.embedder, repo, 16,
		application.WithProgress(report),
		application.WithDedup(dedupThreshold),
		application.WithTokenBudget(c.counter, embedderMaxTokens),
	)

	result, err := service.Ingest(ctx)
	if err != nil {
		return 0, err
	}

	// Reconciliation: prune chunks for repo files that no longer exist (scoped to the repo).
	if spec.ReconcileScope != "" {
		if existing, lerr := repo.ListSources(ctx); lerr != nil {
			c.logger.Warn("reconcile: list sources failed", "err", lerr)
		} else if stale := domain.SourcesToPrune(existing, spec.URLs, spec.ReconcileScope); len(stale) > 0 {
			if derr := repo.DeleteSources(ctx, stale); derr != nil {
				c.logger.Warn("reconcile: delete stale sources failed", "err", derr)
			} else {
				c.logger.Info("reconcile: pruned stale sources", "count", len(stale), "scope", spec.ReconcileScope)
			}
		}
	}
	return result.Ingested, nil
}

type chunkSearcher struct {
	embedder  domain.Embedder
	reranker  domain.Reranker
	qdrantURL string
	apiKey    string
	schema    domain.CollectionSchema
	poolSize  int
	logger    *slog.Logger
}

func (s *chunkSearcher) Search(ctx context.Context, collection, query string, topK int, filter domain.Filter, expand bool) ([]domain.SearchHit, error) {
	repo := qdrant.NewRepository(s.qdrantURL, s.apiKey, collection, s.schema)
	service := application.NewSearchService(s.embedder, repo, s.reranker, s.poolSize, s.logger)
	return service.Search(ctx, query, topK, filter, expand)
}

// memoryStore is the collection-aware write side for agent memory: it builds a
// per-collection Qdrant repository on demand for prepare/replace/delete.
type memoryStore struct {
	qdrantURL string
	apiKey    string
	schema    domain.CollectionSchema
}

func (s *memoryStore) repo(collection string) *qdrant.Repository {
	return qdrant.NewRepository(s.qdrantURL, s.apiKey, collection, s.schema)
}

func (s *memoryStore) Prepare(ctx context.Context, collection string) error {
	return s.repo(collection).Prepare(ctx)
}

func (s *memoryStore) Replace(ctx context.Context, collection, sourceID string, chunks []domain.EmbeddedChunk) error {
	return s.repo(collection).Replace(ctx, sourceID, chunks)
}

func (s *memoryStore) DeleteSources(ctx context.Context, collection string, ids []string) error {
	return s.repo(collection).DeleteSources(ctx, ids)
}

// memorizer adapts *application.MemoryService to the httpapi.Memorizer port.
type memorizer struct {
	svc *application.MemoryService
}

func (m *memorizer) Remember(ctx context.Context, in httpapi.RememberInput) (httpapi.MemoryRecord, error) {
	res, err := m.svc.Remember(ctx, application.RememberParams{
		Collection:         in.Collection,
		Text:               in.Text,
		Author:             in.Author,
		Namespace:          in.Namespace,
		Tags:               in.Tags,
		SupersedeThreshold: in.SupersedeThreshold,
	})
	if err != nil {
		return httpapi.MemoryRecord{}, err
	}
	return httpapi.MemoryRecord{SourceID: res.SourceID, Superseded: res.Superseded, Version: res.Version}, nil
}

func (m *memorizer) Forget(ctx context.Context, collection, id string) error {
	return m.svc.Forget(ctx, collection, id)
}

// memoryLister lists a collection's memories for the Memory UI, adapting Qdrant's
// stored records to the httpapi shape (version string → int).
type memoryLister struct {
	qdrantURL string
	apiKey    string
}

func (l *memoryLister) ListMemories(ctx context.Context, collection string) ([]httpapi.Memory, error) {
	repo := qdrant.NewRepository(l.qdrantURL, l.apiKey, collection, domain.CollectionSchema{})
	stored, err := repo.ListMemories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.Memory, len(stored))
	for i, m := range stored {
		version, _ := strconv.Atoi(m.Version)
		out[i] = httpapi.Memory{
			SourceID:  m.SourceID,
			Document:  m.Document,
			Author:    m.Author,
			UpdatedAt: m.UpdatedAt,
			Version:   version,
			Tags:      m.Tags,
		}
	}
	return out, nil
}

type documentSource struct {
	documents []domain.Document
}

func (d documentSource) Load(context.Context) ([]domain.Document, error) {
	return d.documents, nil
}

type collectionCreator struct {
	qdrantURL string
	apiKey    string
	schema    domain.CollectionSchema
}

func (c *collectionCreator) Create(ctx context.Context, name string) error {
	repo := qdrant.NewRepository(c.qdrantURL, c.apiKey, name, c.schema)
	return repo.Prepare(ctx)
}

type collectionDeleter struct {
	qdrantURL string
	apiKey    string
}

func (d *collectionDeleter) Delete(ctx context.Context, name string) error {
	repo := qdrant.NewRepository(d.qdrantURL, d.apiKey, name, domain.CollectionSchema{})
	return repo.Delete(ctx)
}

type headingsProvider struct {
	qdrantURL string
	apiKey    string
}

func (h *headingsProvider) Headings(ctx context.Context, collection string) ([]string, error) {
	repo := qdrant.NewRepository(h.qdrantURL, h.apiKey, collection, domain.CollectionSchema{})
	return repo.Headings(ctx)
}

type collectionLister struct {
	repo *qdrant.Repository
}

func (c *collectionLister) List(ctx context.Context) ([]httpapi.Collection, error) {
	infos, err := c.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	collections := make([]httpapi.Collection, len(infos))
	for i, info := range infos {
		collections[i] = httpapi.Collection{
			Name:       info.Name,
			Dimension:  info.Dimension,
			VectorName: info.VectorName,
			Points:     info.Points,
		}
	}
	return collections, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

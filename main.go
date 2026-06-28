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
	defaultPoolSize  = 24
)

//go:embed all:web/dist
var webDist embed.FS

func main() {
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", "http://localhost:6333"), "Qdrant REST base URL")
	embedderURL := flag.String("embedder", envOr("EMBEDDER_URL", "http://localhost:8000"), "embedder sidecar base URL")
	addr := flag.String("addr", ":4334", "HTTP listen address")
	poolSize := flag.Int("pool", defaultPoolSize, "rerank candidate pool size (lower = faster search, higher = better recall)")
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

	if err := serveAPI(ctx, *addr, *qdrantURL, os.Getenv("QDRANT_API_KEY"), embedder, schema, model, *poolSize, logger); err != nil {
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
	logger *slog.Logger,
) error {
	ingester := &chunkIngester{embedder: embedder, qdrantURL: qdrantURL, apiKey: apiKey, schema: schema}
	manager := httpapi.NewJobManager(ingester, logger)
	manager.Start()
	defer manager.Stop()

	server := httpapi.NewServer(httpapi.Config{
		Chunker:  markdown.NewSplitter(embedderMaxChars, defaultOverlap),
		Fetcher:  github.NewFetcher(),
		Lister:   &collectionLister{repo: qdrant.NewRepository(qdrantURL, apiKey, "", domain.CollectionSchema{})},
		Creator:  &collectionCreator{qdrantURL: qdrantURL, apiKey: apiKey, schema: schema},
		Searcher: &chunkSearcher{embedder: embedder, reranker: embedder, qdrantURL: qdrantURL, apiKey: apiKey, schema: schema, poolSize: poolSize},
		Jobs:     manager,
		Model:    model,
		UI:       embeddedUI(logger),
		Logger:   logger,
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
	qdrantURL string
	apiKey    string
	schema    domain.CollectionSchema
}

func (c *chunkIngester) Ingest(ctx context.Context, spec httpapi.IngestSpec, report func(processed, total int)) (int, error) {
	document, err := domain.NewDocument(spec.Name, spec.Content)
	if err != nil {
		return 0, err
	}

	maxChars := spec.MaxChars
	if maxChars < 1 {
		maxChars = embedderMaxChars
	}
	overlap := spec.Overlap
	if overlap < 1 {
		overlap = defaultOverlap
	}

	source := documentSource{documents: []domain.Document{document}}
	chunker := markdown.NewSplitter(maxChars, overlap)
	repo := qdrant.NewRepository(c.qdrantURL, c.apiKey, spec.Collection, c.schema)
	service := application.NewIngestService(source, chunker, c.embedder, repo, 16, application.WithProgress(report))

	result, err := service.Ingest(ctx)
	if err != nil {
		return 0, err
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
}

func (s *chunkSearcher) Search(ctx context.Context, collection, query string, topK int, filter domain.Filter) ([]domain.SearchHit, error) {
	repo := qdrant.NewRepository(s.qdrantURL, s.apiKey, collection, s.schema)
	service := application.NewSearchService(s.embedder, repo, s.reranker, s.poolSize)
	return service.Search(ctx, query, topK, filter)
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

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
	"github.com/incu6us/markdex/internal/infrastructure/fastembed"
	"github.com/incu6us/markdex/internal/infrastructure/github"
	"github.com/incu6us/markdex/internal/infrastructure/httpapi"
	"github.com/incu6us/markdex/internal/infrastructure/markdown"
	"github.com/incu6us/markdex/internal/infrastructure/qdrant"
)

const embedderMaxChars = 2000

//go:embed all:web/dist
var webDist embed.FS

func main() {
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", "http://localhost:6333"), "Qdrant REST base URL")
	cacheDir := flag.String("cache", "model_cache", "directory to cache the embedding model")
	addr := flag.String("addr", ":4334", "HTTP listen address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log.Printf("loading embedding model (cache=%s)...", *cacheDir)
	embedder, err := fastembed.New(*cacheDir, embedderMaxChars)
	if err != nil {
		log.Fatalf("load model: %v", err)
	}
	defer embedder.Close()
	log.Printf("model ready, vector dimension=%d", embedder.Dimension())

	if err := serveAPI(ctx, *addr, *qdrantURL, os.Getenv("QDRANT_API_KEY"), embedder); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func serveAPI(ctx context.Context, addr, qdrantURL, apiKey string, embedder *fastembed.Embedder) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	var ui fs.FS
	if dist, err := fs.Sub(webDist, "web/dist"); err == nil {
		if _, statErr := fs.Stat(dist, "index.html"); statErr == nil {
			ui = dist
		} else {
			logger.Warn("embedded web UI not built; serving API only (run `make ui-build`)")
		}
	}

	ingester := &chunkIngester{embedder: embedder, qdrantURL: qdrantURL, apiKey: apiKey}
	manager := httpapi.NewJobManager(ingester, logger)
	manager.Start()
	defer manager.Stop()

	model := httpapi.ModelInfo{Dimension: embedder.Dimension(), VectorName: embedder.VectorName()}
	server := httpapi.NewServer(httpapi.Config{
		Chunker: markdown.NewSplitter(embedderMaxChars, 200),
		Fetcher: github.NewFetcher(),
		Lister:  &collectionLister{repo: qdrant.NewRepository(qdrantURL, apiKey, "", "")},
		Creator: &collectionCreator{qdrantURL: qdrantURL, apiKey: apiKey, model: model},
		Jobs:    manager,
		Model:   model,
		UI:      ui,
		Logger:  logger,
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

type chunkIngester struct {
	embedder  *fastembed.Embedder
	qdrantURL string
	apiKey    string
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

	source := documentSource{documents: []domain.Document{document}}
	chunker := markdown.NewSplitter(maxChars, spec.Overlap)
	repo := qdrant.NewRepository(c.qdrantURL, c.apiKey, spec.Collection, c.embedder.VectorName())
	service := application.NewIngestService(source, chunker, c.embedder, repo, 16, application.WithProgress(report))

	result, err := service.Ingest(ctx)
	if err != nil {
		return 0, err
	}
	return result.Ingested, nil
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
	model     httpapi.ModelInfo
}

func (c *collectionCreator) Create(ctx context.Context, name string) error {
	repo := qdrant.NewRepository(c.qdrantURL, c.apiKey, name, c.model.VectorName)
	return repo.Prepare(ctx, c.model.Dimension)
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

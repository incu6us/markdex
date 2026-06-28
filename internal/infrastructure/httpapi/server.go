package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/incu6us/markdex/internal/domain"
)

type ModelInfo struct {
	Dimension  int
	VectorName string
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
}

// RepoLister lists the raw URLs of every Markdown file in a GitHub repo/path.
type RepoLister interface {
	ListMarkdown(ctx context.Context, repoURL string) ([]string, error)
}

type Config struct {
	Chunker    domain.Chunker
	Fetcher    Fetcher
	RepoLister RepoLister
	Lister     CollectionLister
	Creator    CollectionCreator
	Deleter    CollectionDeleter
	Headings   HeadingsProvider
	Searcher   Searcher
	Jobs       *JobManager
	Model      ModelInfo
	UI         fs.FS
	Logger     *slog.Logger
}

type Server struct {
	chunker    domain.Chunker
	fetcher    Fetcher
	repoLister RepoLister
	lister     CollectionLister
	creator    CollectionCreator
	deleter    CollectionDeleter
	headings   HeadingsProvider
	searcher   Searcher
	jobs       *JobManager
	model      ModelInfo
	ui         fs.FS
	logger     *slog.Logger
}

func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		chunker:    cfg.Chunker,
		fetcher:    cfg.Fetcher,
		repoLister: cfg.RepoLister,
		lister:     cfg.Lister,
		creator:    cfg.Creator,
		deleter:    cfg.Deleter,
		headings:   cfg.Headings,
		searcher:   cfg.Searcher,
		jobs:       cfg.Jobs,
		model:      cfg.Model,
		ui:         cfg.UI,
		logger:     logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/preview", s.handlePreview)
	mux.HandleFunc("GET /api/collections", s.handleCollections)
	mux.HandleFunc("POST /api/collections", s.handleCreateCollection)
	mux.HandleFunc("DELETE /api/collections/{name}", s.handleDeleteCollection)
	mux.HandleFunc("GET /api/collections/{name}/headings", s.handleHeadings)
	mux.HandleFunc("POST /api/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/eval", s.handleEval)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /api/jobs/{id}/stream", s.handleJobStream)
	if s.ui != nil {
		mux.Handle("/", staticHandler(s.ui))
	}
	return mux
}

func staticHandler(ui fs.FS) http.Handler {
	fileServer := http.FileServerFS(ui)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// Vite fingerprints these by content hash, so they never change.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// index.html must be revalidated every load so a redeploy's new asset
		// hashes are picked up instead of a stale cached shell.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, ui, "index.html")
	})
}

type sourceRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

func (s *Server) resolveSource(ctx context.Context, src sourceRequest) (domain.Document, error) {
	switch src.Type {
	case "", "upload":
		return domain.NewDocument(src.Name, src.Content)
	case "github_raw":
		if s.fetcher == nil {
			return domain.Document{}, errors.New("github source is not configured")
		}
		content, err := s.fetcher.Fetch(ctx, src.URL)
		if err != nil {
			return domain.Document{}, err
		}
		return domain.NewDocument(src.URL, content)
	default:
		return domain.Document{}, fmt.Errorf("unsupported source type %q", src.Type)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

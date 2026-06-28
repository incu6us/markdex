package httpapi_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/httpapi"
	"github.com/incu6us/markdex/internal/infrastructure/markdown"
)

func newUIServer(t *testing.T, dir string) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	manager := httpapi.NewJobManager(stubIngester{}, logger)
	manager.Start()
	t.Cleanup(manager.Stop)
	return httpapi.NewServer(httpapi.Config{
		Chunker: markdown.NewSplitter(2000, 200),
		Lister:  stubLister{},
		Jobs:    manager,
		UI:      os.DirFS(dir),
		Logger:  logger,
	}).Handler()
}

func TestStaticUI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>UI</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := newUIServer(t, dir)

	t.Run("serves index at root", func(t *testing.T) {
		rec := doRequest(t, handler, http.MethodGet, "/", nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html><title>UI</title>" {
			t.Fatalf("root = %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("serves built assets", func(t *testing.T) {
		rec := doRequest(t, handler, http.MethodGet, "/assets/app.js", nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
			t.Fatalf("asset = %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("falls back to index for unknown route", func(t *testing.T) {
		rec := doRequest(t, handler, http.MethodGet, "/some/spa/route", nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html><title>UI</title>" {
			t.Fatalf("spa fallback = %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("api routes still take precedence", func(t *testing.T) {
		rec := doRequest(t, handler, http.MethodGet, "/api/jobs/missing", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("api route = %d, want 404 (not the SPA index)", rec.Code)
		}
	})
}

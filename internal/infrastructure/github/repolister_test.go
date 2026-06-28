package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/github"
)

func newAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/git/trees/"):
			if r.URL.Query().Get("recursive") != "1" {
				t.Errorf("trees call missing recursive=1: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"tree":[
				{"path":"README.md","type":"blob"},
				{"path":"docs/guide.md","type":"blob"},
				{"path":"docs/api/ref.MD","type":"blob"},
				{"path":"docs/logo.png","type":"blob"},
				{"path":"docs","type":"tree"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListMarkdownWholeRepo(t *testing.T) {
	t.Parallel()
	api := newAPIServer(t)
	lister := github.NewRepoListerWithBases(api.Client(), api.URL, "https://raw.example", "")

	urls, err := lister.ListMarkdown(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("ListMarkdown: %v", err)
	}
	want := []string{
		"https://raw.example/owner/repo/main/README.md",
		"https://raw.example/owner/repo/main/docs/guide.md",
		"https://raw.example/owner/repo/main/docs/api/ref.MD", // .MD matches case-insensitively
	}
	if len(urls) != len(want) {
		t.Fatalf("got %d urls, want %d: %v", len(urls), len(want), urls)
	}
	for i, w := range want {
		if urls[i] != w {
			t.Fatalf("url[%d] = %q, want %q", i, urls[i], w)
		}
	}
}

func TestListMarkdownSubpathAndBranch(t *testing.T) {
	t.Parallel()
	api := newAPIServer(t)
	lister := github.NewRepoListerWithBases(api.Client(), api.URL, "https://raw.example", "")

	// Branch + subpath in the URL → no default-branch lookup; only docs/ files.
	urls, err := lister.ListMarkdown(context.Background(), "https://github.com/owner/repo/tree/main/docs")
	if err != nil {
		t.Fatalf("ListMarkdown: %v", err)
	}
	for _, u := range urls {
		if !strings.Contains(u, "/main/docs/") {
			t.Fatalf("subpath filter leaked a non-docs file: %q", u)
		}
	}
	if len(urls) != 2 { // docs/guide.md + docs/api/ref.MD
		t.Fatalf("got %d urls under docs/, want 2: %v", len(urls), urls)
	}
}

func TestListMarkdownShorthand(t *testing.T) {
	t.Parallel()
	api := newAPIServer(t)
	lister := github.NewRepoListerWithBases(api.Client(), api.URL, "https://raw.example", "")
	urls, err := lister.ListMarkdown(context.Background(), "owner/repo")
	if err != nil || len(urls) != 3 {
		t.Fatalf("shorthand owner/repo: got %d urls, err %v", len(urls), err)
	}
}

func TestListMarkdownNoMarkdown(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			_, _ = w.Write([]byte(`{"tree":[{"path":"main.go","type":"blob"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	t.Cleanup(api.Close)
	lister := github.NewRepoListerWithBases(api.Client(), api.URL, "https://raw.example", "")
	if _, err := lister.ListMarkdown(context.Background(), "owner/repo"); err == nil {
		t.Fatal("expected error when no .md files are found")
	}
}

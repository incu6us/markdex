package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeRawURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{
			name:  "already raw",
			input: "https://raw.githubusercontent.com/o/r/main/a.md",
			want:  "https://raw.githubusercontent.com/o/r/main/a.md",
		},
		{
			name:  "blob transformed to raw",
			input: "https://github.com/o/r/blob/main/docs/a.md",
			want:  "https://raw.githubusercontent.com/o/r/main/docs/a.md",
		},
		{name: "empty", input: "   ", wantErr: ErrEmptyURL},
		{name: "unsupported host", input: "https://example.com/a.md", wantErr: ErrUnsupportedURL},
		{name: "github non-blob", input: "https://github.com/o/r/tree/main/a.md", wantErr: ErrUnsupportedURL},
		{name: "not markdown", input: "https://raw.githubusercontent.com/o/r/main/a.txt", wantErr: ErrNotMarkdown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRawURL(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetcherGet(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/o/r/main/go.md" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("# Title\nbody"))
	}))
	t.Cleanup(server.Close)

	fetcher := NewFetcher()
	content, err := fetcher.get(context.Background(), server.URL+"/o/r/main/go.md")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if content != "# Title\nbody" {
		t.Fatalf("content = %q", content)
	}
}

func TestFetchWithTokenUsesContentsAPI(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery, gotAuth, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = w.Write([]byte("# Private\nsecret body"))
	}))
	t.Cleanup(server.Close)

	f := NewFetcher()
	f.token = "s3cret"
	f.apiBase = server.URL

	content, err := f.Fetch(context.Background(), "https://raw.githubusercontent.com/o/r/main/docs/a.md")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if content != "# Private\nsecret body" {
		t.Fatalf("content = %q", content)
	}
	if gotPath != "/repos/o/r/contents/docs/a.md" || gotQuery != "ref=main" {
		t.Fatalf("contents request = %s?%s", gotPath, gotQuery)
	}
	if gotAuth != "Bearer s3cret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/vnd.github.raw" {
		t.Fatalf("Accept = %q", gotAccept)
	}
}

func TestFetcherGetNon200(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	if _, err := NewFetcher().get(context.Background(), server.URL+"/missing.md"); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestFetcherRejectsBadURL(t *testing.T) {
	t.Parallel()

	if _, err := NewFetcher().Fetch(context.Background(), "https://example.com/a.md"); !errors.Is(err, ErrUnsupportedURL) {
		t.Fatalf("err = %v, want ErrUnsupportedURL", err)
	}
}

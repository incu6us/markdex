package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxFetchBytes = 5 << 20

var (
	ErrEmptyURL       = errors.New("github url must not be empty")
	ErrUnsupportedURL = errors.New("url must be a raw.githubusercontent.com or github.com blob url")
	ErrNotMarkdown    = errors.New("url must point to a .md file")
)

type Fetcher struct {
	client  *http.Client
	token   string
	apiBase string
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		client:  &http.Client{Timeout: 15 * time.Second},
		token:   os.Getenv("GITHUB_TOKEN"),
		apiBase: "https://api.github.com",
	}
}

func (f *Fetcher) Fetch(ctx context.Context, input string) (string, error) {
	normalized, err := normalizeRawURL(input)
	if err != nil {
		return "", err
	}
	// With a token, fetch via the authenticated contents API so private repos work
	// (raw.githubusercontent.com doesn't accept tokens). Public files still go
	// straight to raw when no token is set.
	if f.token != "" {
		contentsURL, err := rawToContentsURL(f.apiBase, normalized)
		if err != nil {
			return "", err
		}
		return f.do(ctx, contentsURL, map[string]string{
			"Authorization": "Bearer " + f.token,
			"Accept":        "application/vnd.github.raw",
		})
	}
	return f.get(ctx, normalized)
}

func (f *Fetcher) get(ctx context.Context, rawURL string) (string, error) {
	return f.do(ctx, rawURL, nil)
}

func (f *Fetcher) do(ctx context.Context, reqURL string, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d", reqURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", reqURL, err)
	}
	return string(body), nil
}

// rawToContentsURL turns raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}
// into the authenticated contents endpoint apiBase/repos/{owner}/{repo}/contents/{path}?ref={ref}.
func rawToContentsURL(apiBase, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 4)
	if len(parts) < 4 {
		return "", ErrUnsupportedURL
	}
	owner, repo, ref, path := parts[0], parts[1], parts[2], parts[3]
	return fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", apiBase, owner, repo, path, url.QueryEscape(ref)), nil
}

func normalizeRawURL(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", ErrEmptyURL
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", ErrUnsupportedURL
	}

	switch parsed.Host {
	case "raw.githubusercontent.com":
	case "github.com", "www.github.com":
		parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 5)
		if len(parts) < 5 || parts[2] != "blob" {
			return "", ErrUnsupportedURL
		}
		parsed.Host = "raw.githubusercontent.com"
		parsed.Path = "/" + parts[0] + "/" + parts[1] + "/" + parts[3] + "/" + parts[4]
	default:
		return "", ErrUnsupportedURL
	}

	if !strings.HasSuffix(strings.ToLower(parsed.Path), ".md") {
		return "", ErrNotMarkdown
	}
	return parsed.String(), nil
}

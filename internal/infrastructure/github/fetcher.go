package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	client *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *Fetcher) Fetch(ctx context.Context, input string) (string, error) {
	normalized, err := normalizeRawURL(input)
	if err != nil {
		return "", err
	}
	return f.get(ctx, normalized)
}

func (f *Fetcher) get(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rawURL, err)
	}
	return string(body), nil
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

// Package markdexclient is an outbound adapter to the markdex REST API. The MCP
// server (cmd/mcp) uses it to query a running markdex instance over HTTP, so the
// API request/response shapes live in one place instead of being re-declared per
// caller.
package markdexclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client talks to a markdex instance reachable at baseURL.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client targeting baseURL. If httpClient is nil, http.DefaultClient
// is used.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

// Hit is one search result.
type Hit struct {
	Score    float32           `json:"score"`
	Document string            `json:"document"`
	Metadata map[string]string `json:"metadata"`
}

// Collection describes a Qdrant collection known to markdex.
type Collection struct {
	Name       string `json:"name"`
	Dimension  int    `json:"dimension"`
	VectorName string `json:"vector_name"`
	Points     int    `json:"points"`
}

// SearchParams are the inputs to Search.
type SearchParams struct {
	Collection string
	Query      string
	TopK       int
	Expand     bool
}

// Search runs hybrid retrieval + reranking against a collection.
func (c *Client) Search(ctx context.Context, p SearchParams) ([]Hit, error) {
	body, err := json.Marshal(map[string]any{
		"collection": p.Collection,
		"query":      p.Query,
		"top_k":      p.TopK,
		"expand":     p.Expand,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal search request: %w", err)
	}

	var out struct {
		Results []Hit `json:"results"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/search", bytes.NewReader(body), &out); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return out.Results, nil
}

// ListCollections returns all collections known to markdex.
func (c *Client) ListCollections(ctx context.Context) ([]Collection, error) {
	var out struct {
		Collections []Collection `json:"collections"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/collections", nil, &out); err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	return out.Collections, nil
}

// ListHeadings returns the heading paths present in a collection, useful for
// discovering valid heading_path filters before searching.
func (c *Client) ListHeadings(ctx context.Context, collection string) ([]string, error) {
	path := "/api/collections/" + url.PathEscape(collection) + "/headings"
	var out struct {
		Headings []string `json:"headings"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("list headings: %w", err)
	}
	return out.Headings, nil
}

// RememberParams are the inputs to Remember.
type RememberParams struct {
	Collection string
	Text       string
	Author     string
	Namespace  string
	Tags       string
	// SupersedeThreshold optionally overrides the server default for this write.
	SupersedeThreshold *float64
}

// RememberResult reports what the server did with the memory.
type RememberResult struct {
	SourceID   string `json:"source_id"`
	Superseded bool   `json:"superseded"`
	Version    int    `json:"version"`
}

// Remember stores a fact in a collection, superseding a near-identical existing
// memory in place or appending a new one.
func (c *Client) Remember(ctx context.Context, p RememberParams) (RememberResult, error) {
	payload := map[string]any{
		"collection": p.Collection,
		"text":       p.Text,
		"author":     p.Author,
		"namespace":  p.Namespace,
		"tags":       p.Tags,
	}
	if p.SupersedeThreshold != nil {
		payload["supersede_threshold"] = *p.SupersedeThreshold
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return RememberResult{}, fmt.Errorf("marshal remember request: %w", err)
	}
	var out RememberResult
	if err := c.do(ctx, http.MethodPost, "/api/memories", bytes.NewReader(body), &out); err != nil {
		return RememberResult{}, fmt.Errorf("remember: %w", err)
	}
	return out, nil
}

// Forget deletes a memory by its source_id from a collection.
func (c *Client) Forget(ctx context.Context, collection, id string) error {
	path := "/api/memories/" + url.PathEscape(id) + "?collection=" + url.QueryEscape(collection)
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("forget: %w", err)
	}
	return nil
}

// do performs an HTTP request against the markdex API and decodes a JSON response.
func (c *Client) do(ctx context.Context, method, path string, body *bytes.Reader, out any) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = body
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface the markdex error body (e.g. {"error":"..."}) so a calling
		// agent can self-correct; cap it so a runaway body can't blow up the message.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return fmt.Errorf("status %d: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if out == nil {
		return nil // e.g. 204 No Content (Forget)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

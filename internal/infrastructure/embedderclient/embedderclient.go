package embedderclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/incu6us/markdex/internal/domain"
)

var (
	_ domain.Embedder = (*Client)(nil)
	_ domain.Reranker = (*Client)(nil)
)

type Client struct {
	baseURL string
	client  *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

type Info struct {
	DenseDim    int    `json:"dense_dim"`
	DenseName   string `json:"dense_name"`
	SparseName  string `json:"sparse_name"`
	EmbedModel  string `json:"embed_model"`
	RerankModel string `json:"rerank_model"`
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var info Info
	if err := c.do(ctx, http.MethodGet, "/info", nil, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (c *Client) Ready(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

type embedRequest struct {
	Texts []string `json:"texts"`
	Kind  string   `json:"kind"`
}

type sparseDTO struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

type embedResponse struct {
	Dense  [][]float32 `json:"dense"`
	Sparse []sparseDTO `json:"sparse"`
}

func (c *Client) Embed(ctx context.Context, texts []string, kind domain.EmbedKind) ([]domain.Vectors, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var resp embedResponse
	if err := c.do(ctx, http.MethodPost, "/embed", embedRequest{Texts: texts, Kind: kind.String()}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Dense) != len(texts) || len(resp.Sparse) != len(texts) {
		return nil, fmt.Errorf("embedder returned %d dense / %d sparse for %d texts", len(resp.Dense), len(resp.Sparse), len(texts))
	}

	vectors := make([]domain.Vectors, len(texts))
	for i := range texts {
		dense, err := domain.NewEmbedding(resp.Dense[i])
		if err != nil {
			return nil, fmt.Errorf("dense vector %d: %w", i, err)
		}
		sparse, err := domain.NewSparseEmbedding(resp.Sparse[i].Indices, resp.Sparse[i].Values)
		if err != nil {
			return nil, fmt.Errorf("sparse vector %d: %w", i, err)
		}
		vectors[i] = domain.Vectors{Dense: dense, Sparse: sparse}
	}
	return vectors, nil
}

type rerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      *int     `json:"top_k,omitempty"`
}

type rerankResult struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

func (c *Client) Rerank(ctx context.Context, query string, documents []string, topK int) ([]domain.Ranked, error) {
	if len(documents) == 0 {
		return nil, nil
	}

	req := rerankRequest{Query: query, Documents: documents}
	if topK > 0 {
		req.TopK = &topK
	}

	var resp rerankResponse
	if err := c.do(ctx, http.MethodPost, "/rerank", req, &resp); err != nil {
		return nil, err
	}

	ranked := make([]domain.Ranked, len(resp.Results))
	for i, result := range resp.Results {
		ranked[i] = domain.Ranked{Index: result.Index, Score: result.Score}
	}
	return ranked, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("embedder %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("embedder %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

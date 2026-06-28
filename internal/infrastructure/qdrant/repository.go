package qdrant

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

const (
	maxAttempts = 3
	retryDelay  = 500 * time.Millisecond
)

type Repository struct {
	baseURL    string
	apiKey     string
	collection string
	vectorName string
	client     *http.Client
}

func NewRepository(baseURL, apiKey, collection, vectorName string) *Repository {
	return &Repository{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		collection: collection,
		vectorName: vectorName,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Repository) Prepare(ctx context.Context, dimension int) error {
	exists, err := r.collectionExists(ctx)
	if err != nil {
		return fmt.Errorf("prepare collection %q: %w", r.collection, err)
	}
	if exists {
		return nil
	}

	body := map[string]any{
		"vectors": map[string]any{
			r.vectorName: map[string]any{
				"size":     dimension,
				"distance": "Cosine",
			},
		},
	}
	if err := r.do(ctx, http.MethodPut, "/collections/"+r.collection, body, nil); err != nil {
		return fmt.Errorf("prepare collection %q: %w", r.collection, err)
	}
	return nil
}

type CollectionInfo struct {
	Name       string
	Dimension  int
	VectorName string
	Points     int
}

func (r *Repository) List(ctx context.Context) ([]CollectionInfo, error) {
	var listed struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := r.do(ctx, http.MethodGet, "/collections", nil, &listed); err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}

	infos := make([]CollectionInfo, 0, len(listed.Result.Collections))
	for _, collection := range listed.Result.Collections {
		info, err := r.describe(ctx, collection.Name)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (r *Repository) describe(ctx context.Context, name string) (CollectionInfo, error) {
	var described struct {
		Result struct {
			PointsCount int `json:"points_count"`
			Config      struct {
				Params struct {
					Vectors map[string]struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := r.do(ctx, http.MethodGet, "/collections/"+name, nil, &described); err != nil {
		return CollectionInfo{}, fmt.Errorf("describe collection %q: %w", name, err)
	}

	info := CollectionInfo{Name: name, Points: described.Result.PointsCount}
	for vectorName, vector := range described.Result.Config.Params.Vectors {
		info.VectorName = vectorName
		info.Dimension = vector.Size
		break
	}
	return info, nil
}

func (r *Repository) collectionExists(ctx context.Context) (bool, error) {
	var out struct {
		Result struct {
			Exists bool `json:"exists"`
		} `json:"result"`
	}
	if err := r.do(ctx, http.MethodGet, "/collections/"+r.collection+"/exists", nil, &out); err != nil {
		return false, err
	}
	return out.Result.Exists, nil
}

func (r *Repository) Replace(ctx context.Context, sourceID string, chunks []domain.EmbeddedChunk) error {
	if err := r.deleteBySource(ctx, sourceID); err != nil {
		return fmt.Errorf("replace source %q in %q: %w", sourceID, r.collection, err)
	}
	if len(chunks) == 0 {
		return nil
	}

	points := make([]map[string]any, 0, len(chunks))
	for _, ec := range chunks {
		chunk := ec.Chunk
		points = append(points, map[string]any{
			"id":     chunk.ID(),
			"vector": map[string]any{r.vectorName: ec.Embedding.Vector()},
			"payload": map[string]any{
				"document": chunk.Content(),
				"metadata": map[string]any{
					"path":         chunk.SourceID(),
					"source_id":    chunk.SourceID(),
					"title":        chunk.Title(),
					"heading_path": chunk.HeadingPath(),
					"chunk_index":  chunk.Index(),
				},
			},
		})
	}

	body := map[string]any{"points": points}
	path := "/collections/" + r.collection + "/points?wait=true"
	if err := r.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("save %d points into %q: %w", len(chunks), r.collection, err)
	}
	return nil
}

func (r *Repository) deleteBySource(ctx context.Context, sourceID string) error {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "metadata.source_id", "match": map[string]any{"value": sourceID}},
			},
		},
	}
	path := "/collections/" + r.collection + "/points/delete?wait=true"
	return r.do(ctx, http.MethodPost, path, body, nil)
}

func (r *Repository) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		marshalled, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		payload = marshalled
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = r.attempt(ctx, method, path, payload, out)
		if lastErr == nil {
			return nil
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay * time.Duration(attempt)):
			}
		}
	}
	return lastErr
}

func (r *Repository) attempt(ctx context.Context, method, path string, payload []byte, out any) error {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, r.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.apiKey != "" {
		req.Header.Set("api-key", r.apiKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}

	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("qdrant %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
}

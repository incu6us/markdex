package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/incu6us/markdex/internal/domain"
)

const defaultSearchTopK = 8

type Searcher interface {
	Search(ctx context.Context, collection, query string, topK int, filter domain.Filter, expand bool) ([]domain.SearchHit, error)
}

type searchRequest struct {
	Collection string            `json:"collection"`
	Query      string            `json:"query"`
	TopK       int               `json:"top_k"`
	Filter     map[string]string `json:"filter"`
	Expand     bool              `json:"expand"`
}

type searchHit struct {
	ID       string            `json:"id"`
	Score    float32           `json:"score"`
	Document string            `json:"document"`
	Metadata map[string]string `json:"metadata"`
}

type searchResponse struct {
	Results []searchHit `json:"results"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Collection) == "" || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "collection and query are required")
		return
	}

	topK := req.TopK
	if topK < 1 {
		topK = defaultSearchTopK
	}

	hits, err := s.searcher.Search(r.Context(), req.Collection, req.Query, topK, domain.Filter{Match: req.Filter}, req.Expand)
	if err != nil {
		s.logger.Error("search failed", "collection", req.Collection, "err", err)
		writeError(w, http.StatusBadGateway, "search failed")
		return
	}

	results := make([]searchHit, len(hits))
	for i, hit := range hits {
		results[i] = searchHit{ID: hit.ID, Score: hit.Score, Document: hit.Document, Metadata: hit.Metadata}
	}
	writeJSON(w, http.StatusOK, searchResponse{Results: results})
}

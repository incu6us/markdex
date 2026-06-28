package httpapi

import (
	"net/http"
	"strings"

	"github.com/incu6us/markdex/internal/application"
	"github.com/incu6us/markdex/internal/domain"
)

type evalQuery struct {
	Query                   string   `json:"query"`
	RelevantHeadingContains []string `json:"relevant_heading_contains"`
}

type evalRequest struct {
	Collection string      `json:"collection"`
	TopK       int         `json:"top_k"`
	Queries    []evalQuery `json:"queries"`
}

type evalQueryResult struct {
	Query string `json:"query"`
	Rank  int    `json:"rank"`
}

type evalMetrics struct {
	Queries int     `json:"queries"`
	MRR     float64 `json:"mrr"`
	HitAt1  float64 `json:"hit_at_1"`
	HitAt3  float64 `json:"hit_at_3"`
	HitAtK  float64 `json:"hit_at_k"`
}

type evalResponse struct {
	TopK    int               `json:"top_k"`
	Metrics evalMetrics       `json:"metrics"`
	Results []evalQueryResult `json:"results"`
}

func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
	var req evalRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Collection) == "" || len(req.Queries) == 0 {
		writeError(w, http.StatusBadRequest, "collection and queries are required")
		return
	}
	topK := req.TopK
	if topK < 1 {
		topK = application.DefaultEvalTopK
	}

	ranks := make([]int, len(req.Queries))
	results := make([]evalQueryResult, len(req.Queries))
	for i, q := range req.Queries {
		hits, err := s.searcher.Search(r.Context(), req.Collection, q.Query, topK, domain.Filter{}, false)
		if err != nil {
			s.logger.Error("eval search failed", "collection", req.Collection, "query", q.Query, "err", err)
			writeError(w, http.StatusBadGateway, "search failed")
			return
		}
		paths := make([]string, len(hits))
		for j, hit := range hits {
			paths[j] = hit.Metadata["heading_path"]
		}
		rank := application.FirstRelevantRank(paths, q.RelevantHeadingContains)
		ranks[i] = rank
		results[i] = evalQueryResult{Query: q.Query, Rank: rank}
	}

	m := application.Aggregate(ranks, topK)
	writeJSON(w, http.StatusOK, evalResponse{
		TopK: topK,
		Metrics: evalMetrics{
			Queries: m.Queries, MRR: m.MRR, HitAt1: m.HitAt1, HitAt3: m.HitAt3, HitAtK: m.HitAtK,
		},
		Results: results,
	})
}

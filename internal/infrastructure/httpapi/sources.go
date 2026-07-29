package httpapi

import (
	"context"
	"net/http"
	"strings"
)

// Source is one ingested document in a collection, aggregated from its chunks.
// Ingested chunks carry no timestamp, so Chunks is the only size signal.
type Source struct {
	SourceID string `json:"source_id"`
	Title    string `json:"title"`
	Chunks   int    `json:"chunks"`
}

// SourceLister lists a collection's ingested documents (everything that is not
// an agent memory).
type SourceLister interface {
	ListSources(ctx context.Context, collection string) ([]Source, error)
}

// SourceDeleter removes an ingested document and all of its chunks.
type SourceDeleter interface {
	DeleteSource(ctx context.Context, collection, sourceID string) error
}

type sourcesResponse struct {
	Sources []Source `json:"sources"`
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	if s.sourceLister == nil {
		writeJSON(w, http.StatusOK, sourcesResponse{Sources: []Source{}})
		return
	}
	if !checkCollectionName(w, r.PathValue("name")) {
		return
	}
	sources, err := s.sourceLister.ListSources(r.Context(), r.PathValue("name"))
	if err != nil {
		s.logger.Error("list sources failed", "collection", r.PathValue("name"), "err", err)
		writeError(w, http.StatusBadGateway, "failed to list sources")
		return
	}
	if sources == nil {
		sources = []Source{}
	}
	writeJSON(w, http.StatusOK, sourcesResponse{Sources: sources})
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	if !checkCollectionName(w, r.PathValue("name")) {
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	if sourceID == "" {
		writeError(w, http.StatusBadRequest, "?source_id is required")
		return
	}
	if s.sourceDeleter == nil {
		writeError(w, http.StatusNotImplemented, "source deletion is not configured")
		return
	}
	if err := s.sourceDeleter.DeleteSource(r.Context(), r.PathValue("name"), sourceID); err != nil {
		s.logger.Error("delete source failed", "collection", r.PathValue("name"), "source_id", sourceID, "err", err)
		writeError(w, http.StatusBadGateway, "failed to delete source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

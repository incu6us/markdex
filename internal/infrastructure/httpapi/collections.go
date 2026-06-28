package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var errMismatch = errors.New("collection vector dimension or name does not match the embedding model")

type Collection struct {
	Name       string `json:"name"`
	Dimension  int    `json:"dimension"`
	VectorName string `json:"vector_name"`
	Points     int    `json:"points"`
}

type CollectionLister interface {
	List(ctx context.Context) ([]Collection, error)
}

type CollectionCreator interface {
	Create(ctx context.Context, name string) error
}

type collectionsResponse struct {
	Collections []Collection `json:"collections"`
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	collections, err := s.lister.List(r.Context())
	if err != nil {
		s.logger.Error("list collections failed", "err", err)
		writeError(w, http.StatusBadGateway, "failed to list collections")
		return
	}
	writeJSON(w, http.StatusOK, collectionsResponse{Collections: collections})
}

type createCollectionRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req createCollectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "collection name is required")
		return
	}

	if err := s.creator.Create(r.Context(), req.Name); err != nil {
		s.logger.Error("create collection failed", "name", req.Name, "err", err)
		writeError(w, http.StatusBadGateway, "failed to create collection")
		return
	}
	writeJSON(w, http.StatusCreated, Collection{
		Name:       req.Name,
		Dimension:  s.model.Dimension,
		VectorName: s.model.VectorName,
	})
}

func (s *Server) validateTarget(ctx context.Context, collection string) error {
	collections, err := s.lister.List(ctx)
	if err != nil {
		s.logger.Warn("collection validation skipped: list failed", "err", err)
		return nil
	}
	for _, existing := range collections {
		if existing.Name != collection {
			continue
		}
		if existing.Dimension != s.model.Dimension || existing.VectorName != s.model.VectorName {
			return errMismatch
		}
		return nil
	}
	return nil
}

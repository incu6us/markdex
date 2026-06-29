package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// RememberInput carries one agent memory to store.
type RememberInput struct {
	Collection string
	Text       string
	Author     string
	Namespace  string
	Tags       string
	// SupersedeThreshold optionally overrides the server default for this write.
	SupersedeThreshold *float64
}

// MemoryRecord reports the outcome of a remember.
type MemoryRecord struct {
	SourceID   string
	Superseded bool
	Version    int
}

// Memorizer is the agent write-back port: store a fact (supersede-or-append) and
// delete one by id.
type Memorizer interface {
	Remember(ctx context.Context, in RememberInput) (MemoryRecord, error)
	Forget(ctx context.Context, collection, id string) error
}

// Memory is one stored memory returned by the list endpoint.
type Memory struct {
	SourceID  string `json:"source_id"`
	Document  string `json:"document"`
	Author    string `json:"author,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Version   int    `json:"version,omitempty"`
	Tags      string `json:"tags,omitempty"`
}

// MemoryLister lists the memories (type="memory") in a collection, newest first.
type MemoryLister interface {
	ListMemories(ctx context.Context, collection string) ([]Memory, error)
}

type rememberRequest struct {
	Collection         string   `json:"collection"`
	Text               string   `json:"text"`
	Author             string   `json:"author"`
	Namespace          string   `json:"namespace"`
	Tags               string   `json:"tags"`
	SupersedeThreshold *float64 `json:"supersede_threshold,omitempty"`
}

type rememberResponse struct {
	SourceID   string `json:"source_id"`
	Superseded bool   `json:"superseded"`
	Version    int    `json:"version"`
}

func (s *Server) handleRemember(w http.ResponseWriter, r *http.Request) {
	if s.memorizer == nil {
		writeError(w, http.StatusNotImplemented, "memory write-back is not configured")
		return
	}

	var req rememberRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Collection) == "" || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "collection and text are required")
		return
	}
	if req.SupersedeThreshold != nil && (*req.SupersedeThreshold <= 0 || *req.SupersedeThreshold > 1) {
		writeError(w, http.StatusBadRequest, "supersede_threshold must be in (0, 1]")
		return
	}

	// Reuse the ingest dim/model pre-check: writing into an existing collection with
	// a mismatched vector is rejected up front (a new collection is created to fit).
	if err := s.validateTarget(r.Context(), req.Collection); errors.Is(err, errMismatch) {
		writeError(w, http.StatusConflict, errMismatch.Error())
		return
	}

	record, err := s.memorizer.Remember(r.Context(), RememberInput{
		Collection:         req.Collection,
		Text:               req.Text,
		Author:             req.Author,
		Namespace:          req.Namespace,
		Tags:               req.Tags,
		SupersedeThreshold: req.SupersedeThreshold,
	})
	if err != nil {
		s.logger.Error("remember failed", "collection", req.Collection, "err", err)
		writeError(w, http.StatusBadGateway, "failed to store memory")
		return
	}
	writeJSON(w, http.StatusCreated, rememberResponse{
		SourceID:   record.SourceID,
		Superseded: record.Superseded,
		Version:    record.Version,
	})
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	if s.memoryLister == nil {
		writeJSON(w, http.StatusOK, map[string]any{"memories": []Memory{}})
		return
	}
	memories, err := s.memoryLister.ListMemories(r.Context(), r.PathValue("name"))
	if err != nil {
		s.logger.Error("list memories failed", "collection", r.PathValue("name"), "err", err)
		writeError(w, http.StatusBadGateway, "failed to list memories")
		return
	}
	if memories == nil {
		memories = []Memory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	if s.memorizer == nil {
		writeError(w, http.StatusNotImplemented, "memory write-back is not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	collection := strings.TrimSpace(r.URL.Query().Get("collection"))
	if id == "" || collection == "" {
		writeError(w, http.StatusBadRequest, "memory id and ?collection are required")
		return
	}

	if err := s.memorizer.Forget(r.Context(), collection, id); err != nil {
		s.logger.Error("forget failed", "collection", collection, "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "failed to delete memory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireAuth gates the write endpoints. TODO(auth): enforce a token here — for now
// it is a deliberate pass-through no-op (write-back ships open; auth is the immediate
// follow-up, and routing every write through this one seam keeps it a single diff).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return next
}

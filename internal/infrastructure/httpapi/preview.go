package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/incu6us/markdex/internal/domain"
)

type previewRequest struct {
	Source sourceRequest `json:"source"`
}

type topic struct {
	Title       string `json:"title"`
	HeadingPath string `json:"heading_path"`
	Chunks      int    `json:"chunks"`
	Chars       int    `json:"chars"`
}

type previewResponse struct {
	Name        string  `json:"name"`
	TotalChunks int     `json:"total_chunks"`
	Topics      []topic `json:"topics"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	doc, err := s.resolveSource(r.Context(), req.Source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	chunks, err := s.chunker.Split(doc)
	if err != nil {
		s.logger.Error("preview split failed", "source", doc.Path(), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to split document")
		return
	}

	writeJSON(w, http.StatusOK, previewResponse{
		Name:        doc.Path(),
		TotalChunks: len(chunks),
		Topics:      topicsFrom(chunks),
	})
}

func topicsFrom(chunks []domain.Chunk) []topic {
	topics := make([]topic, 0)
	for _, chunk := range chunks {
		top := chunk.HeadingPath()
		if i := strings.IndexByte(top, '/'); i >= 0 {
			top = top[:i]
		}

		if n := len(topics); n > 0 && topics[n-1].Title == chunk.Title() && topics[n-1].HeadingPath == top {
			topics[n-1].Chunks++
			topics[n-1].Chars += utf8.RuneCountInString(chunk.Content())
			continue
		}
		topics = append(topics, topic{
			Title:       chunk.Title(),
			HeadingPath: top,
			Chunks:      1,
			Chars:       utf8.RuneCountInString(chunk.Content()),
		})
	}
	return topics
}

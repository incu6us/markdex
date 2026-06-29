package domain

import (
	"errors"
	"maps"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrEmptySourceID = errors.New("chunk source id must not be empty")
	ErrNegativeIndex = errors.New("chunk index must not be negative")
)

type ChunkParams struct {
	SourceID    string
	Index       int
	Title       string
	HeadingPath string
	Content     string
	// Metadata is an optional free-form bag merged into the stored payload's
	// metadata alongside the reserved keys (path, source_id, title, heading_path,
	// chunk_index). Reserved keys are always set from the typed fields above and
	// cannot be overridden from here. Used for agent memory (type, author, …).
	Metadata map[string]string
}

type Chunk struct {
	sourceID    string
	index       int
	title       string
	headingPath string
	content     string
	metadata    map[string]string
}

func NewChunk(p ChunkParams) (Chunk, error) {
	if strings.TrimSpace(p.SourceID) == "" {
		return Chunk{}, ErrEmptySourceID
	}
	if strings.TrimSpace(p.Content) == "" {
		return Chunk{}, ErrEmptyContent
	}
	if p.Index < 0 {
		return Chunk{}, ErrNegativeIndex
	}
	return Chunk{
		sourceID:    p.SourceID,
		index:       p.Index,
		title:       p.Title,
		headingPath: p.HeadingPath,
		content:     p.Content,
		metadata:    copyMetadata(p.Metadata),
	}, nil
}

// copyMetadata returns a defensive copy of m so a Chunk never aliases its
// caller's map. An empty/nil map stays nil.
func copyMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return maps.Clone(m)
}

func (c Chunk) ID() string {
	return uuid.NewSHA1(pathNamespace, []byte(c.sourceID+"#"+strconv.Itoa(c.index))).String()
}

// NewID returns a fresh random identifier, used to mint a unique source_id for a
// new agent memory ("memory://<id>").
func NewID() string {
	return uuid.NewString()
}

func (c Chunk) SourceID() string { return c.sourceID }

func (c Chunk) Index() int { return c.index }

func (c Chunk) Title() string { return c.title }

func (c Chunk) HeadingPath() string { return c.headingPath }

func (c Chunk) Content() string { return c.content }

// Metadata returns the chunk's optional free-form metadata bag (nil if none).
func (c Chunk) Metadata() map[string]string { return c.metadata }

// ContextualText returns the chunk content prefixed with a human-readable
// breadcrumb of its heading path (contextual retrieval), so the embedding
// encodes where the chunk sits in the document and the reranker can separate
// near-identical sections. The stored document keeps Content() only.
func (c Chunk) ContextualText() string {
	return ContextualText(c.headingPath, c.content)
}

// ContextualText prefixes content with a human-readable breadcrumb of headingPath,
// or returns content unchanged when there is no heading path. Shared by ingest
// (what gets embedded) and search (what gets reranked) so both see the same text.
func ContextualText(headingPath, content string) string {
	breadcrumb := humanizeHeadingPath(headingPath)
	if breadcrumb == "" {
		return content
	}
	return breadcrumb + "\n\n" + content
}

// humanizeHeadingPath turns a slug heading path ("a/b-c/d") into a readable
// breadcrumb ("a > b c > d") so the embedding model sees natural words.
func humanizeHeadingPath(headingPath string) string {
	headingPath = strings.TrimSpace(headingPath)
	if headingPath == "" {
		return ""
	}
	segments := strings.Split(headingPath, "/")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(segment, "-", " ")
	}
	return strings.Join(segments, " > ")
}

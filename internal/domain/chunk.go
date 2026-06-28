package domain

import (
	"errors"
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
}

type Chunk struct {
	sourceID    string
	index       int
	title       string
	headingPath string
	content     string
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
	}, nil
}

func (c Chunk) ID() string {
	return uuid.NewSHA1(pathNamespace, []byte(c.sourceID+"#"+strconv.Itoa(c.index))).String()
}

func (c Chunk) SourceID() string { return c.sourceID }

func (c Chunk) Index() int { return c.index }

func (c Chunk) Title() string { return c.title }

func (c Chunk) HeadingPath() string { return c.headingPath }

func (c Chunk) Content() string { return c.content }

// ContextualText returns the chunk content prefixed with a human-readable
// breadcrumb of its heading path (contextual retrieval), so the embedding
// encodes where the chunk sits in the document and the reranker can separate
// near-identical sections. The stored document keeps Content() only.
func (c Chunk) ContextualText() string {
	breadcrumb := humanizeHeadingPath(c.headingPath)
	if breadcrumb == "" {
		return c.content
	}
	return breadcrumb + "\n\n" + c.content
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

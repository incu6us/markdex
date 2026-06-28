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

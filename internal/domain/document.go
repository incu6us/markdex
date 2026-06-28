package domain

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

var pathNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

var (
	ErrEmptyPath    = errors.New("document path must not be empty")
	ErrEmptyContent = errors.New("document content must not be empty")
)

type Document struct {
	path    string
	content string
}

func NewDocument(path, content string) (Document, error) {
	if strings.TrimSpace(path) == "" {
		return Document{}, ErrEmptyPath
	}
	if strings.TrimSpace(content) == "" {
		return Document{}, ErrEmptyContent
	}
	return Document{path: path, content: content}, nil
}

func (d Document) ID() string {
	return uuid.NewSHA1(pathNamespace, []byte(d.path)).String()
}

func (d Document) Path() string { return d.path }

func (d Document) Content() string { return d.content }

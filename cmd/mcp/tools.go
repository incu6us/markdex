package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/incu6us/markdex/internal/infrastructure/markdexclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// markdexService is the slice of the markdex API the MCP tools depend on. The
// concrete *markdexclient.Client satisfies it; tests inject a fake.
type markdexService interface {
	Search(ctx context.Context, p markdexclient.SearchParams) ([]markdexclient.Hit, error)
	ListCollections(ctx context.Context) ([]markdexclient.Collection, error)
	ListHeadings(ctx context.Context, collection string) ([]string, error)
	Remember(ctx context.Context, p markdexclient.RememberParams) (markdexclient.RememberResult, error)
	Forget(ctx context.Context, collection, id string) error
}

// toolDeps carries the dependencies shared by every tool handler.
type toolDeps struct {
	svc markdexService
}

// --- search ---

// top_k is normalized at the tool boundary: missing/non-positive falls back to
// defaultTopK, and oversized requests are clamped to maxTopK so an agent can't
// make the backend (and reranker) do unbounded work.
const (
	defaultTopK = 8
	maxTopK     = 100
)

type searchInput struct {
	Collection string `json:"collection" jsonschema:"collection name to search"`
	Query      string `json:"query" jsonschema:"natural-language query"`
	TopK       int    `json:"top_k,omitempty" jsonschema:"number of results (default 8, max 100)"`
	Expand     bool   `json:"expand,omitempty" jsonschema:"return the full enclosing section instead of the matched chunk"`
}

type searchResult struct {
	HeadingPath string  `json:"heading_path"`
	Score       float32 `json:"score"`
	Document    string  `json:"document"`
}

type searchOutput struct {
	Results []searchResult `json:"results"`
}

func (d *toolDeps) search(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
	if strings.TrimSpace(in.Collection) == "" || strings.TrimSpace(in.Query) == "" {
		return nil, searchOutput{}, errors.New("collection and query are required")
	}
	if in.TopK < 1 {
		in.TopK = defaultTopK
	}
	if in.TopK > maxTopK {
		in.TopK = maxTopK
	}

	hits, err := d.svc.Search(ctx, markdexclient.SearchParams{
		Collection: in.Collection,
		Query:      in.Query,
		TopK:       in.TopK,
		Expand:     in.Expand,
	})
	if err != nil {
		return nil, searchOutput{}, err
	}

	out := searchOutput{Results: make([]searchResult, len(hits))}
	for i, h := range hits {
		out.Results[i] = searchResult{HeadingPath: h.Metadata["heading_path"], Score: h.Score, Document: h.Document}
	}
	return textResult(formatSearchResults(hits)), out, nil
}

// --- list_collections ---

type collectionsOutput struct {
	Collections []markdexclient.Collection `json:"collections"`
}

func (d *toolDeps) listCollections(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, collectionsOutput, error) {
	cols, err := d.svc.ListCollections(ctx)
	if err != nil {
		return nil, collectionsOutput{}, err
	}
	return textResult(formatCollections(cols)), collectionsOutput{Collections: cols}, nil
}

// --- list_headings ---

type headingsInput struct {
	Collection string `json:"collection" jsonschema:"collection name to list heading paths for"`
}

type headingsOutput struct {
	Headings []string `json:"headings"`
}

func (d *toolDeps) listHeadings(ctx context.Context, _ *mcp.CallToolRequest, in headingsInput) (*mcp.CallToolResult, headingsOutput, error) {
	if strings.TrimSpace(in.Collection) == "" {
		return nil, headingsOutput{}, errors.New("collection is required")
	}
	heads, err := d.svc.ListHeadings(ctx, in.Collection)
	if err != nil {
		return nil, headingsOutput{}, err
	}
	return textResult(formatHeadings(in.Collection, heads)), headingsOutput{Headings: heads}, nil
}

// --- remember ---

type rememberInput struct {
	Collection         string   `json:"collection" jsonschema:"collection to store the memory in"`
	Text               string   `json:"text" jsonschema:"the fact to remember, as a short self-contained statement"`
	Author             string   `json:"author,omitempty" jsonschema:"who is recording this (agent or user id)"`
	Namespace          string   `json:"namespace,omitempty" jsonschema:"optional scope, e.g. team:payments"`
	Tags               string   `json:"tags,omitempty" jsonschema:"optional comma-separated tags"`
	SupersedeThreshold *float64 `json:"supersede_threshold,omitempty" jsonschema:"advanced: rerank-score cutoff (0,1] to replace a near-identical memory; omit to use the server default"`
}

type rememberOutput struct {
	SourceID   string `json:"source_id"`
	Superseded bool   `json:"superseded"`
	Version    int    `json:"version"`
}

func (d *toolDeps) remember(ctx context.Context, _ *mcp.CallToolRequest, in rememberInput) (*mcp.CallToolResult, rememberOutput, error) {
	if strings.TrimSpace(in.Collection) == "" || strings.TrimSpace(in.Text) == "" {
		return nil, rememberOutput{}, errors.New("collection and text are required")
	}
	if in.SupersedeThreshold != nil && (*in.SupersedeThreshold <= 0 || *in.SupersedeThreshold > 1) {
		return nil, rememberOutput{}, errors.New("supersede_threshold must be in (0, 1]")
	}
	res, err := d.svc.Remember(ctx, markdexclient.RememberParams{
		Collection:         in.Collection,
		Text:               in.Text,
		Author:             in.Author,
		Namespace:          in.Namespace,
		Tags:               in.Tags,
		SupersedeThreshold: in.SupersedeThreshold,
	})
	if err != nil {
		return nil, rememberOutput{}, err
	}
	out := rememberOutput{SourceID: res.SourceID, Superseded: res.Superseded, Version: res.Version}
	verb := "Stored"
	if res.Superseded {
		verb = "Updated (superseded a near-identical memory)"
	}
	return textResult(fmt.Sprintf("%s memory %s (v%d).", verb, res.SourceID, res.Version)), out, nil
}

// --- forget ---

type forgetInput struct {
	Collection string `json:"collection" jsonschema:"collection the memory is in"`
	ID         string `json:"id" jsonschema:"the memory source_id to delete (as returned by remember)"`
}

type forgetOutput struct {
	Deleted bool `json:"deleted"`
}

func (d *toolDeps) forget(ctx context.Context, _ *mcp.CallToolRequest, in forgetInput) (*mcp.CallToolResult, forgetOutput, error) {
	if strings.TrimSpace(in.Collection) == "" || strings.TrimSpace(in.ID) == "" {
		return nil, forgetOutput{}, errors.New("collection and id are required")
	}
	if err := d.svc.Forget(ctx, in.Collection, in.ID); err != nil {
		return nil, forgetOutput{}, err
	}
	return textResult(fmt.Sprintf("Deleted memory %s.", in.ID)), forgetOutput{Deleted: true}, nil
}

// --- registration ---

// register wires every markdex tool onto the MCP server. The tools are read-only
// and reach an external markdex instance, so they advertise the matching hints.
func (d *toolDeps) register(s *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(s, &mcp.Tool{
		Name: "search",
		Description: "Search a markdex knowledge-base collection with hybrid (dense+sparse) retrieval " +
			"and cross-encoder reranking. Returns the most relevant document chunks with their section " +
			"path and relevance score. Use list_collections first if you do not know the collection name.",
		Annotations: readOnly,
	}, d.search)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_collections",
		Description: "List the knowledge-base collections available in markdex, with their dimension and point count.",
		Annotations: readOnly,
	}, d.listCollections)

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_headings",
		Description: "List the heading paths present in a collection. Use this to discover a collection's " +
			"structure and valid heading_path values before searching.",
		Annotations: readOnly,
	}, d.listHeadings)

	// Write tools: agent memory. Not read-only; remember supersedes a near-identical
	// existing memory in place (or appends), forget deletes one by id.
	mcp.AddTool(s, &mcp.Tool{
		Name: "remember",
		Description: "Store a fact in a markdex collection so future searches (and agents) can retrieve it. " +
			"A near-identical existing memory is replaced in place; otherwise a new one is appended. " +
			"Use for durable knowledge an agent learns: rules, conventions, business facts.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, OpenWorldHint: ptr(true)},
	}, d.remember)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "forget",
		Description: "Delete a memory from a collection by its source_id (as returned by remember).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, d.forget)
}

// --- formatting ---

func formatSearchResults(hits []markdexclient.Hit) string {
	if len(hits) == 0 {
		return "No results."
	}
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "## [%d] %s  (score %.3f)\n%s\n\n", i+1, h.Metadata["heading_path"], h.Score, h.Document)
	}
	return strings.TrimSpace(b.String())
}

func formatCollections(cols []markdexclient.Collection) string {
	if len(cols) == 0 {
		return "No collections."
	}
	var b strings.Builder
	for _, c := range cols {
		fmt.Fprintf(&b, "- %s (%d points, dim %d)\n", c.Name, c.Points, c.Dimension)
	}
	return strings.TrimSpace(b.String())
}

func formatHeadings(collection string, heads []string) string {
	if len(heads) == 0 {
		return fmt.Sprintf("No headings in %q.", collection)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Heading paths in %q:\n", collection)
	for _, h := range heads {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	return strings.TrimSpace(b.String())
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func ptr[T any](v T) *T { return &v }

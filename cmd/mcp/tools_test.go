package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/markdexclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeService struct {
	hits      []markdexclient.Hit
	cols      []markdexclient.Collection
	heads     []string
	err       error
	gotSearch markdexclient.SearchParams
	gotColl   string
}

func (f *fakeService) Search(_ context.Context, p markdexclient.SearchParams) ([]markdexclient.Hit, error) {
	f.gotSearch = p
	return f.hits, f.err
}

func (f *fakeService) ListCollections(context.Context) ([]markdexclient.Collection, error) {
	return f.cols, f.err
}

func (f *fakeService) ListHeadings(_ context.Context, collection string) ([]string, error) {
	f.gotColl = collection
	return f.heads, f.err
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("result has no content: %+v", res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func TestSearchHandlerDefaultsTopKAndMaps(t *testing.T) {
	t.Parallel()
	fake := &fakeService{hits: []markdexclient.Hit{
		{Score: 0.91, Document: "body", Metadata: map[string]string{"heading_path": "go/naming"}},
	}}
	d := &toolDeps{svc: fake}

	res, out, err := d.search(context.Background(), &mcp.CallToolRequest{}, searchInput{Collection: "c", Query: "naming"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if fake.gotSearch.TopK != 8 {
		t.Fatalf("TopK default = %d, want 8", fake.gotSearch.TopK)
	}
	if len(out.Results) != 1 || out.Results[0].HeadingPath != "go/naming" || out.Results[0].Score != 0.91 {
		t.Fatalf("structured output = %+v", out)
	}
	if !strings.Contains(textOf(t, res), "go/naming") {
		t.Fatalf("text = %q", textOf(t, res))
	}
}

func TestSearchHandlerValidation(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{}}
	if _, _, err := d.search(context.Background(), &mcp.CallToolRequest{}, searchInput{Query: "no collection"}); err == nil {
		t.Fatal("expected validation error for missing collection")
	}
}

func TestSearchHandlerPropagatesClientError(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{err: errors.New("boom")}}
	if _, _, err := d.search(context.Background(), &mcp.CallToolRequest{}, searchInput{Collection: "c", Query: "q"}); err == nil {
		t.Fatal("expected client error to propagate")
	}
}

func TestListCollectionsHandler(t *testing.T) {
	t.Parallel()
	fake := &fakeService{cols: []markdexclient.Collection{{Name: "go-style-guide", Dimension: 1024, Points: 115}}}
	d := &toolDeps{svc: fake}

	res, out, err := d.listCollections(context.Background(), &mcp.CallToolRequest{}, struct{}{})
	if err != nil {
		t.Fatalf("listCollections: %v", err)
	}
	if len(out.Collections) != 1 || out.Collections[0].Name != "go-style-guide" {
		t.Fatalf("output = %+v", out)
	}
	if !strings.Contains(textOf(t, res), "go-style-guide") {
		t.Fatalf("text = %q", textOf(t, res))
	}
}

func TestListHeadingsHandler(t *testing.T) {
	t.Parallel()
	fake := &fakeService{heads: []string{"go/naming", "go/errors"}}
	d := &toolDeps{svc: fake}

	res, out, err := d.listHeadings(context.Background(), &mcp.CallToolRequest{}, headingsInput{Collection: "c"})
	if err != nil {
		t.Fatalf("listHeadings: %v", err)
	}
	if fake.gotColl != "c" {
		t.Fatalf("collection passed = %q", fake.gotColl)
	}
	if len(out.Headings) != 2 || !strings.Contains(textOf(t, res), "go/errors") {
		t.Fatalf("output = %+v text = %q", out, textOf(t, res))
	}
}

func TestListHeadingsValidation(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{}}
	if _, _, err := d.listHeadings(context.Background(), &mcp.CallToolRequest{}, headingsInput{}); err == nil {
		t.Fatal("expected validation error for missing collection")
	}
}

func TestFormatSearchResultsEmpty(t *testing.T) {
	t.Parallel()
	if got := formatSearchResults(nil); got != "No results." {
		t.Fatalf("empty format = %q", got)
	}
}

func TestFormatCollectionsEmpty(t *testing.T) {
	t.Parallel()
	if got := formatCollections(nil); !strings.Contains(strings.ToLower(got), "no collections") {
		t.Fatalf("empty format = %q", got)
	}
}

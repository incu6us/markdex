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
	hits        []markdexclient.Hit
	cols        []markdexclient.Collection
	heads       []string
	err         error
	gotSearch   markdexclient.SearchParams
	gotColl     string
	rememberRes markdexclient.RememberResult
	gotRemember markdexclient.RememberParams
	gotForgetID string
	gotForgetCo string
}

func (f *fakeService) Remember(_ context.Context, p markdexclient.RememberParams) (markdexclient.RememberResult, error) {
	f.gotRemember = p
	return f.rememberRes, f.err
}

func (f *fakeService) Forget(_ context.Context, collection, id string) error {
	f.gotForgetCo, f.gotForgetID = collection, id
	return f.err
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

func TestSearchHandlerClampsTopK(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	d := &toolDeps{svc: fake}

	if _, _, err := d.search(context.Background(), &mcp.CallToolRequest{}, searchInput{Collection: "c", Query: "q", TopK: 100_000}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if fake.gotSearch.TopK != maxTopK {
		t.Fatalf("TopK = %d, want clamped to %d", fake.gotSearch.TopK, maxTopK)
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

func TestRememberHandler(t *testing.T) {
	t.Parallel()
	fake := &fakeService{rememberRes: markdexclient.RememberResult{SourceID: "memory:abc", Superseded: true, Version: 2}}
	d := &toolDeps{svc: fake}

	res, out, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{
		Collection: "team-memory", Text: "Acme is on legacy billing", Author: "agent:claude-code",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if fake.gotRemember.Collection != "team-memory" || fake.gotRemember.Text != "Acme is on legacy billing" || fake.gotRemember.Author != "agent:claude-code" {
		t.Fatalf("client got %+v", fake.gotRemember)
	}
	if out.SourceID != "memory:abc" || !out.Superseded || out.Version != 2 {
		t.Fatalf("structured output = %+v", out)
	}
	if !strings.Contains(textOf(t, res), "memory:abc") {
		t.Fatalf("text = %q", textOf(t, res))
	}
}

func TestRememberHandlerPassesThreshold(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	d := &toolDeps{svc: fake}
	th := 0.9
	if _, _, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{
		Collection: "c", Text: "x", SupersedeThreshold: &th,
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if fake.gotRemember.SupersedeThreshold == nil || *fake.gotRemember.SupersedeThreshold != 0.9 {
		t.Fatalf("threshold passed = %v, want 0.9", fake.gotRemember.SupersedeThreshold)
	}
}

func TestRememberHandlerThresholdValidation(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{}}
	bad := 1.5
	if _, _, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{
		Collection: "c", Text: "x", SupersedeThreshold: &bad,
	}); err == nil {
		t.Fatal("expected validation error for out-of-range threshold")
	}
}

func TestRememberHandlerValidation(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{}}
	if _, _, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{Text: "no collection"}); err == nil {
		t.Fatal("expected validation error for missing collection")
	}
	if _, _, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{Collection: "c"}); err == nil {
		t.Fatal("expected validation error for missing text")
	}
}

func TestRememberHandlerPropagatesError(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{err: errors.New("boom")}}
	if _, _, err := d.remember(context.Background(), &mcp.CallToolRequest{}, rememberInput{Collection: "c", Text: "x"}); err == nil {
		t.Fatal("expected client error to propagate")
	}
}

func TestForgetHandler(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	d := &toolDeps{svc: fake}

	res, _, err := d.forget(context.Background(), &mcp.CallToolRequest{}, forgetInput{Collection: "team-memory", ID: "memory:abc"})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if fake.gotForgetCo != "team-memory" || fake.gotForgetID != "memory:abc" {
		t.Fatalf("client got %q in %q", fake.gotForgetID, fake.gotForgetCo)
	}
	if !strings.Contains(textOf(t, res), "memory:abc") {
		t.Fatalf("text = %q", textOf(t, res))
	}
}

func TestForgetHandlerValidation(t *testing.T) {
	t.Parallel()
	d := &toolDeps{svc: &fakeService{}}
	if _, _, err := d.forget(context.Background(), &mcp.CallToolRequest{}, forgetInput{ID: "memory:abc"}); err == nil {
		t.Fatal("expected validation error for missing collection")
	}
	if _, _, err := d.forget(context.Background(), &mcp.CallToolRequest{}, forgetInput{Collection: "c"}); err == nil {
		t.Fatal("expected validation error for missing id")
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

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/markdexclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectE2E wires a real markdexclient -> real MCP server -> in-memory client
// session, against a fake markdex backend. It returns the connected client
// session for driving real tools/call round-trips.
func connectE2E(t *testing.T, backend http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)

	deps := &toolDeps{svc: markdexclient.New(srv.URL, srv.Client())}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "markdex", Version: "test"}, nil)
	deps.register(mcpServer)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpServer.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("no content in result: %+v", res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func TestE2ESearchReturnsTextAndStructuredContent(t *testing.T) {
	t.Parallel()
	cs := connectE2E(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"1","score":0.91,"document":"body","metadata":{"heading_path":"go/naming"}}]}`))
	})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"collection": "c", "query": "naming"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, res))
	}
	// Unstructured text the model reads.
	if got := resultText(t, res); got == "" || !contains(got, "go/naming") {
		t.Fatalf("text content = %q", got)
	}
	// Structured content the SDK generated from the typed Out value.
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %T, want object", res.StructuredContent)
	}
	results, ok := sc["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("structured results = %#v", sc["results"])
	}
	first := results[0].(map[string]any)
	if first["heading_path"] != "go/naming" {
		t.Fatalf("structured result = %#v", first)
	}
}

func TestE2EListCollectionsAndHeadings(t *testing.T) {
	t.Parallel()
	cs := connectE2E(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/collections":
			_, _ = w.Write([]byte(`{"collections":[{"name":"go-style-guide","dimension":1024,"points":115}]}`))
		case "/api/collections/go-style-guide/headings":
			_, _ = w.Write([]byte(`{"headings":["go/naming","go/errors"]}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()

	cols, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_collections"})
	if err != nil || cols.IsError {
		t.Fatalf("list_collections err=%v isError=%v", err, cols.IsError)
	}
	if !contains(resultText(t, cols), "go-style-guide") {
		t.Fatalf("collections text = %q", resultText(t, cols))
	}

	heads, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_headings",
		Arguments: map[string]any{"collection": "go-style-guide"},
	})
	if err != nil || heads.IsError {
		t.Fatalf("list_headings err=%v isError=%v", err, heads.IsError)
	}
	if !contains(resultText(t, heads), "go/errors") {
		t.Fatalf("headings text = %q", resultText(t, heads))
	}
}

func TestE2ESearchBackendErrorSetsIsError(t *testing.T) {
	t.Parallel()
	cs := connectE2E(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"collection not found"}`, http.StatusBadGateway)
	})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"collection": "missing", "query": "q"},
	})
	if err != nil {
		t.Fatalf("CallTool transport err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true when backend fails")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

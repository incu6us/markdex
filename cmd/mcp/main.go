// Command mcp is a Model Context Protocol (stdio) server exposing markdex retrieval
// to agents (e.g. Claude Code). It is a thin client of markdex's REST API, built on
// the official github.com/modelcontextprotocol/go-sdk, and offers three read-only
// tools: search, list_collections, and list_headings.
//
// Register it (with markdex running):
//
//	claude mcp add markdex -e MARKDEX_URL=http://localhost:4334 -- go run ./cmd/mcp
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/incu6us/markdex/internal/infrastructure/markdexclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	base := os.Getenv("MARKDEX_URL")
	if base == "" {
		base = "http://localhost:4334"
	}

	logger := log.New(os.Stderr, "markdex-mcp: ", log.LstdFlags)
	logger.Printf("serving over stdio, markdex at %s", base)

	deps := &toolDeps{svc: markdexclient.New(base, &http.Client{Timeout: 60 * time.Second})}
	srv := mcp.NewServer(&mcp.Implementation{Name: "markdex", Version: "0.2.0"}, nil)
	deps.register(srv)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		logger.Fatalf("server stopped: %v", err)
	}
}

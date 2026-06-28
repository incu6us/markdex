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
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/incu6us/markdex/internal/infrastructure/markdexclient"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// codeServerClosing is the JSON-RPC error code the SDK reports when the stdio
// client closes the connection (its internal ErrServerClosing). Matching the
// code is stable across SDK versions, unlike its message text.
const codeServerClosing = -32004

func main() {
	base := os.Getenv("MARKDEX_URL")
	if base == "" {
		base = "http://localhost:4334"
	}

	logger := log.New(os.Stderr, "markdex-mcp: ", log.LstdFlags)
	logger.Printf("serving over stdio, markdex at %s", base)

	// Shut down cleanly on SIGINT/SIGTERM; cancelling the context unwinds Run.
	// (stdin EOF, the normal stdio lifecycle, also returns from Run.)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := &toolDeps{svc: markdexclient.New(base, &http.Client{Timeout: 60 * time.Second})}
	srv := mcp.NewServer(&mcp.Implementation{Name: "markdex", Version: "0.2.0"}, nil)
	deps.register(srv)

	if err := srv.Run(ctx, &mcp.StdioTransport{}); !isCleanShutdown(err) {
		logger.Fatalf("server stopped: %v", err)
	}
	logger.Print("shutting down")
}

// isCleanShutdown reports whether a Server.Run error represents the normal end of
// a stdio session rather than a failure: a nil error, our own signal-driven
// context cancellation, the input reaching EOF, or the client closing the pipe
// (which the SDK surfaces as the codeServerClosing JSON-RPC error).
func isCleanShutdown(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == codeServerClosing
}

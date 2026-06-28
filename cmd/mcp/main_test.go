package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIsCleanShutdown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"context canceled", context.Canceled, true},
		{"eof", io.EOF, true},
		{"server closing wire error", &jsonrpc.Error{Code: codeServerClosing, Message: "server is closing"}, true},
		{"wrapped server closing", errors.Join(errors.New("run"), &jsonrpc.Error{Code: codeServerClosing}), true},
		{"other wire error", &jsonrpc.Error{Code: -32603, Message: "internal error"}, false},
		{"genuine failure", errors.New("dial tcp: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isCleanShutdown(tc.err); got != tc.want {
				t.Fatalf("isCleanShutdown(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRunReturnsCleanOnClientDisconnect drives the real server through a full
// initialize handshake and then closes the input (EOF) — the same shape as a
// stdio client disconnecting — and asserts Run's error is classified as a clean
// shutdown (the SDK surfaces this as the codeServerClosing wire error, not EOF).
func TestRunReturnsCleanOnClientDisconnect(t *testing.T) {
	t.Parallel()
	srv := mcp.NewServer(&mcp.Implementation{Name: "markdex", Version: "test"}, nil)
	(&toolDeps{svc: &fakeService{}}).register(srv)

	const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
		`"params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	transport := &mcp.IOTransport{
		Reader: &scriptedReader{data: []byte(initMsg)},
		Writer: discardWriteCloser{},
	}

	err := srv.Run(context.Background(), transport)
	if !isCleanShutdown(err) {
		t.Fatalf("client disconnect should be a clean shutdown, got %v", err)
	}
}

// scriptedReader yields its data once, then reports EOF — mimicking a client that
// sends a handshake and then closes the connection.
type scriptedReader struct {
	data []byte
	pos  int
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *scriptedReader) Close() error { return nil }

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

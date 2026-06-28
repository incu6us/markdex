// Command mcp is a minimal Model Context Protocol (stdio) server exposing markdex retrieval
// as a `search` tool, so agents (e.g. Claude Code) can query a collection with hybrid search +
// reranking. It is a thin client of markdex's POST /api/search.
//
// Register it (with markdex running):
//
//	claude mcp add markdex -e MARKDEX_URL=http://localhost:4334 -- go run ./cmd/mcp
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type searchFunc func(collection, query string, topK int, expand bool) (string, error)

type server struct {
	search searchFunc
}

var searchTool = map[string]any{
	"name": "search",
	"description": "Search a markdex knowledge-base collection with hybrid (dense+sparse) retrieval " +
		"and cross-encoder reranking. Returns the most relevant document chunks with their section " +
		"path and relevance score.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": map[string]any{"type": "string", "description": "collection name to search"},
			"query":      map[string]any{"type": "string", "description": "natural-language query"},
			"top_k":      map[string]any{"type": "integer", "description": "number of results (default 8)"},
			"expand":     map[string]any{"type": "boolean", "description": "return the full enclosing section instead of the matched chunk"},
		},
		"required": []string{"collection", "query"},
	},
}

// dispatch handles one JSON-RPC message and returns the response plus whether to send it
// (notifications get no response).
func (s *server) dispatch(req rpcRequest) (rpcResponse, bool) {
	switch req.Method {
	case "initialize":
		return result(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "markdex", "version": "0.1.0"},
		}), true
	case "notifications/initialized":
		return rpcResponse{}, false
	case "ping":
		return result(req.ID, map[string]any{}), true
	case "tools/list":
		return result(req.ID, map[string]any{"tools": []any{searchTool}}), true
	case "tools/call":
		return s.callTool(req), true
	default:
		if len(req.ID) == 0 {
			return rpcResponse{}, false // unknown notification
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}, true
	}
}

func (s *server) callTool(req rpcRequest) rpcResponse {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			Collection string `json:"collection"`
			Query      string `json:"query"`
			TopK       int    `json:"top_k"`
			Expand     bool   `json:"expand"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &params)

	if params.Name != "search" {
		return toolError(req.ID, "unknown tool: "+params.Name)
	}
	args := params.Arguments
	if strings.TrimSpace(args.Collection) == "" || strings.TrimSpace(args.Query) == "" {
		return toolError(req.ID, "collection and query are required")
	}
	if args.TopK < 1 {
		args.TopK = 8
	}

	text, err := s.search(args.Collection, args.Query, args.TopK, args.Expand)
	if err != nil {
		return toolError(req.ID, err.Error())
	}
	return result(req.ID, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	})
}

func result(id json.RawMessage, payload any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: payload}
}

func toolError(id json.RawMessage, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"isError": true,
		"content": []any{map[string]any{"type": "text", "text": msg}},
	}}
}

func httpSearch(base string, client *http.Client) searchFunc {
	return func(collection, query string, topK int, expand bool) (string, error) {
		payload, _ := json.Marshal(map[string]any{"collection": collection, "query": query, "top_k": topK, "expand": expand})
		resp, err := client.Post(base+"/api/search", "application/json", bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("markdex search: status %d", resp.StatusCode)
		}

		var out struct {
			Results []struct {
				Score    float32           `json:"score"`
				Document string            `json:"document"`
				Metadata map[string]string `json:"metadata"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", err
		}
		if len(out.Results) == 0 {
			return "No results.", nil
		}

		var b strings.Builder
		for i, r := range out.Results {
			fmt.Fprintf(&b, "## [%d] %s  (score %.3f)\n%s\n\n", i+1, r.Metadata["heading_path"], r.Score, r.Document)
		}
		return strings.TrimSpace(b.String()), nil
	}
}

func main() {
	base := os.Getenv("MARKDEX_URL")
	if base == "" {
		base = "http://localhost:4334"
	}
	srv := &server{search: httpSearch(base, &http.Client{Timeout: 60 * time.Second})}

	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var req rpcRequest
			if json.Unmarshal(line, &req) == nil {
				if resp, send := srv.dispatch(req); send {
					_ = encoder.Encode(resp)
				}
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			return
		}
	}
}

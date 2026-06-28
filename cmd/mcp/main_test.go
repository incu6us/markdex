package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func req(t *testing.T, id, method string, params any) rpcRequest {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	var idRaw json.RawMessage
	if id != "" {
		idRaw = json.RawMessage(id)
	}
	return rpcRequest{JSONRPC: "2.0", ID: idRaw, Method: method, Params: raw}
}

func TestDispatchInitialize(t *testing.T) {
	t.Parallel()
	s := &server{}
	resp, send := s.dispatch(req(t, "1", "initialize", nil))
	if !send {
		t.Fatal("initialize must respond")
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %v", m["protocolVersion"])
	}
	if m["serverInfo"].(map[string]any)["name"] != "markdex" {
		t.Fatalf("serverInfo = %v", m["serverInfo"])
	}
}

func TestDispatchInitializedNotification(t *testing.T) {
	t.Parallel()
	s := &server{}
	_, send := s.dispatch(req(t, "", "notifications/initialized", nil))
	if send {
		t.Fatal("notifications must not respond")
	}
}

func TestDispatchToolsList(t *testing.T) {
	t.Parallel()
	s := &server{}
	resp, _ := s.dispatch(req(t, "2", "tools/list", nil))
	tools := resp.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "search" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestDispatchToolsCall(t *testing.T) {
	t.Parallel()

	var gotCollection, gotQuery string
	var gotTopK int
	var gotExpand bool
	s := &server{search: func(collection, query string, topK int, expand bool) (string, error) {
		gotCollection, gotQuery, gotTopK, gotExpand = collection, query, topK, expand
		return "## [1] go/interfaces  (score 0.900)\nbody", nil
	}}

	resp, _ := s.dispatch(req(t, "3", "tools/call", map[string]any{
		"name":      "search",
		"arguments": map[string]any{"collection": "c", "query": "interfaces", "expand": true},
	}))

	if gotCollection != "c" || gotQuery != "interfaces" || gotTopK != 8 || !gotExpand {
		t.Fatalf("search got %q/%q/%d/%v", gotCollection, gotQuery, gotTopK, gotExpand)
	}
	content := resp.Result.(map[string]any)["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "go/interfaces") {
		t.Fatalf("tool result text = %q", text)
	}
}

func TestDispatchToolsCallValidation(t *testing.T) {
	t.Parallel()
	s := &server{search: func(string, string, int, bool) (string, error) {
		t.Fatal("search must not be called without required args")
		return "", nil
	}}
	resp, _ := s.dispatch(req(t, "4", "tools/call", map[string]any{
		"name": "search", "arguments": map[string]any{"query": "no collection"},
	}))
	if resp.Result.(map[string]any)["isError"] != true {
		t.Fatalf("expected isError result, got %v", resp.Result)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	t.Parallel()
	s := &server{}
	resp, send := s.dispatch(req(t, "5", "no/such", nil))
	if !send || resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected method-not-found error, got %+v", resp)
	}
}

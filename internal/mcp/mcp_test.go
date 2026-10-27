package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRPCStdio(t *testing.T) {
	s := Server{Version: "1.0.0", Tools: []Tool{{Name: "echo", Description: "echo", Schema: Schema([]string{"value"}, map[string]any{"value": Field("string", "value")}), Handler: func(_ context.Context, args map[string]any) (any, error) {
		return map[string]any{"result": args["value"]}, nil
	}}}}
	requests := strings.Join([]string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}`, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"value":"привет"}}}`, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"missing"}}`}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(requests), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 replies, got %d: %s", len(lines), out.String())
	}
	var r map[string]any
	if e := json.Unmarshal([]byte(lines[0]), &r); e != nil {
		t.Fatal(e)
	}
	result := r["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatal("bad negotiation")
	}
	if e := json.Unmarshal([]byte(lines[1]), &r); e != nil {
		t.Fatal(e)
	}
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatal("missing tools")
	}
	if e := json.Unmarshal([]byte(lines[2]), &r); e != nil {
		t.Fatal(e)
	}
	content := r["result"].(map[string]any)["content"].([]any)
	if !strings.Contains(content[0].(map[string]any)["text"].(string), "привет") {
		t.Fatal("missing echo content")
	}
	if e := json.Unmarshal([]byte(lines[3]), &r); e != nil {
		t.Fatal(e)
	}
	if r["error"] == nil {
		t.Fatal("unknown tool didn't error")
	}
}

func TestModernMCP2026(t *testing.T) {
	s := Server{Version: "1.0.0", Tools: []Tool{{Name: "read", Description: "read", Schema: Schema(nil, map[string]any{}), Handler: func(_ context.Context, a map[string]any) (any, error) { return map[string]any{"ok": true}, nil }}}}
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"copilot","version":"1"}}`
	requests := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta + `}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{` + meta + `}}` + "\n" + `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{},` + meta + `}}` + "\n"
	var out bytes.Buffer
	if e := s.Serve(context.Background(), strings.NewReader(requests), &out); e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrong response count %s", out.String())
	}
	for _, line := range lines {
		var r map[string]any
		if e := json.Unmarshal([]byte(line), &r); e != nil {
			t.Fatal(e)
		}
		result := r["result"].(map[string]any)
		if result["resultType"] != "complete" {
			t.Fatal("modern resultType missing")
		}
		if result["_meta"] == nil {
			t.Fatal("serverInfo missing")
		}
	}
	var list map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &list)
	res := list["result"].(map[string]any)
	if res["cacheScope"] != "private" || res["ttlMs"] != float64(0) {
		t.Fatal("cache hints required for modern tools/list")
	}
}

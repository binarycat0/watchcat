package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	s := &Server{Name: "watchdog", Version: "test", Instructions: "use watch_pr",
		Tools: []Tool{
			{Name: "echo", Description: "echo back", Handler: func(ctx context.Context, a json.RawMessage) (string, error) {
				var p struct{ Msg string }
				json.Unmarshal(a, &p)
				return "got " + p.Msg, nil
			}},
			{Name: "boom", Description: "fails", Handler: func(context.Context, json.RawMessage) (string, error) {
				return "", errors.New("daemon not running")
			}},
		}}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"bogus"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 { // the notification gets no reply
		t.Fatalf("want 7 replies, got %d:\n%s", len(lines), out.String())
	}
	var r []map[string]any
	for _, l := range lines {
		var m map[string]any
		json.Unmarshal([]byte(l), &m)
		r = append(r, m)
	}
	init := r[0]["result"].(map[string]any)
	if init["protocolVersion"] != ProtocolVersion || init["instructions"] != "use watch_pr" {
		t.Fatalf("initialize: %v", init)
	}
	tools := r[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Fatalf("tools/list: %v", tools)
	}
	text := func(m map[string]any) (string, bool) {
		res := m["result"].(map[string]any)
		return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
	}
	if s, isErr := text(r[2]); s != "got hi" || isErr {
		t.Fatalf("echo: %q %v", s, isErr)
	}
	if s, isErr := text(r[3]); s != "daemon not running" || !isErr {
		t.Fatalf("tool error should be an isError result: %q %v", s, isErr)
	}
	if r[4]["error"] == nil || r[5]["error"].(map[string]any)["code"].(float64) != -32601 || r[6]["error"] == nil {
		t.Fatalf("errors: %v %v %v", r[4], r[5], r[6])
	}
}

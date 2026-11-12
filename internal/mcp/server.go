// Package mcp is a minimal Model Context Protocol server over stdio:
// newline-delimited JSON-RPC 2.0 with initialize, tools/list and tools/call.
// That's all agents need to discover and call watchcat's tools; each tool's
// description and input schema are its "help".
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the MCP revision this server implements.
const ProtocolVersion = "2025-06-18"

// Tool is one callable tool. Handler returns text for the agent; an error
// becomes a tool result with isError set, which the agent sees and can act
// on (as opposed to a protocol error).
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server answers MCP requests for a fixed set of tools.
type Server struct {
	Name         string
	Version      string
	Instructions string // shown to the agent on connect: what this server is for
	Tools        []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests from r and writes responses to w until r ends or
// ctx is cancelled. Requests are handled one at a time, in order.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	var mu sync.Mutex
	write := func(resp response) error {
		b, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_, err = w.Write(append(b, '\n'))
		return err
	}
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // notification (e.g. notifications/initialized): no reply
		}
		result, rerr := s.handle(ctx, req)
		resp := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
		if err := write(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, len(s.Tools))
		for i, t := range s.Tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools[i] = map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema}
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			args := p.Arguments
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			text, err := t.Handler(ctx, args)
			if err != nil {
				return toolResult(err.Error(), true), nil
			}
			return toolResult(text, false), nil
		}
		return nil, &rpcError{-32602, fmt.Sprintf("unknown tool %q", p.Name)}
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

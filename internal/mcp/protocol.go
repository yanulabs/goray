// Package mcp implements the stdio MCP JSON-RPC transport.
package mcp

import (
	"context"
	"encoding/json"
)

// Request is one JSON-RPC request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolAnnotations are the optional MCP behavioral hints clients use to
// auto-approve safe tools and warn before destructive or open-world calls.
// Hints are always emitted so clients never fall back to protocol defaults.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// ToolSpec is the protocol-facing description of one tool.
type ToolSpec struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// Tool couples protocol metadata with a handler. Exclusive tools bypass the
// concurrent read pool (test/lint/reindex).
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Annotations *ToolAnnotations
	Exclusive   bool
	Run         func(context.Context, map[string]any) (string, bool)
}

func (t Tool) spec() ToolSpec {
	return ToolSpec{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, Annotations: t.Annotations}
}

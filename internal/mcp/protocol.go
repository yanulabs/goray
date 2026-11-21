// Package mcp implements the stdio MCP JSON-RPC transport.
package mcp

import (
	"context"
	"encoding/json"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Tool couples protocol metadata with a handler. Exclusive tools bypass the
// concurrent read pool (test/lint/reindex).
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Exclusive   bool
	Run         func(context.Context, map[string]any) (string, bool)
}

func (t Tool) spec() ToolSpec {
	return ToolSpec{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
}

// Package tools defines the MCP tools exposed by the server.
package tools

import (
	"context"
	"encoding/json"

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
)

// Indexer is the index capability required by the tools.
type Indexer interface {
	Wait(context.Context) (*index.Index, error)
	Status() index.Status
	Root() string
	Rebuild() error
}

type toolset struct {
	src   Indexer
	count int
}

// New returns all tools in stable order.
func New(src Indexer) []mcp.Tool {
	t := &toolset{src: src}
	list := []mcp.Tool{
		t.projectInfo(),
		t.codebaseMap(),
		t.moduleOverview(),
		t.dependencyGraph(),
		t.lookupSymbol(),
		t.apiSurface(),
		t.references(),
		t.callGraph(),
		t.implementations(),
		t.impactAnalysis(),
		t.deadCode(),
		t.functionBody(),
		t.runTests(),
		t.runLint(),
		t.reindex(),
	}
	t.count = len(list)
	return list
}

func (t *toolset) indexed(run func(*index.Index, map[string]any) (string, bool)) func(context.Context, map[string]any) (string, bool) {
	return func(ctx context.Context, args map[string]any) (string, bool) {
		idx, err := t.src.Wait(ctx)
		if err != nil {
			return err.Error(), true
		}
		return run(idx, args)
	}
}

func (t *toolset) projectInfo() mcp.Tool {
	return mcp.Tool{
		Name:        "project_info",
		Description: "Index readiness, module path, package/symbol/xref counts, load errors, and available tools.",
		InputSchema: object(nil),
		Run: func(context.Context, map[string]any) (string, bool) {
			info := struct {
				index.Status
				Tools int `json:"tools"`
			}{Status: t.src.Status(), Tools: t.count}
			return toJSON(info), false
		},
	}
}

func object(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func propString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func propInt(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func propBool(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func propEnum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func strArg(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func intArg(args map[string]any, key string, fallback int) int {
	if v, ok := args[key].(float64); ok && int(v) > 0 {
		return int(v)
	}
	return fallback
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return fallback
}

func toJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(fallback)
	}
	return string(data)
}

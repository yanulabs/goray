// Package tools defines the MCP tools exposed by the server.
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

// Indexer is the index capability required by the tools.
type Indexer interface {
	Wait(context.Context) (*index.Index, error)
	Status() index.Status
	Diagnostics() []index.Diagnostic
	Root() string
	RebuildContext(context.Context) error
}

// Summaries is the summary-cache capability required by the tools.
type Summaries interface {
	Enrich(context.Context, string) (json.RawMessage, cache.SyncState, bool, error)
	ListStale(context.Context, cache.StaleFilter) ([]cache.StaleSymbol, error)
	SummaryContext(context.Context, string, *index.Index) (cache.SummaryContext, error)
	UpsertSummaries(context.Context, []cache.SummaryInput) ([]cache.UpsertResult, error)
	Coverage(context.Context, string) (cache.Coverage, error)
	Path() string
	SummaryPath() string
}

type toolset struct {
	src       Indexer
	formats   *render.Registry
	summaries Summaries
	count     int
}

// New returns all tools in stable order.
func New(src Indexer, formats *render.Registry, summaries Summaries) []mcp.Tool {
	if formats == nil {
		formats = render.NewRegistry()
	}
	t := &toolset{src: src, formats: formats, summaries: summaries}
	list := []mcp.Tool{
		t.projectInfo(),
		t.codebaseMap(),
		t.moduleOverview(),
		t.dependencyGraph(),
		t.explainDependency(),
		t.lookupSymbol(),
		t.inspectType(),
		t.apiSurface(),
		t.references(),
		t.callGraph(),
		t.findCallPath(),
		t.implementations(),
		t.impactAnalysis(),
		t.deadCode(),
		t.hotspots(),
		t.functionBody(),
		t.listStaleSummaries(),
		t.getSummaryContext(),
		t.upsertSummaries(),
		t.getSummaryCoverage(),
		t.diagnostics(),
		t.architectureDiff(),
		t.runTests(),
		t.runLint(),
		t.reindex(),
	}
	t.count = len(list)
	return list
}

func indexingPayload(err error) (string, bool) {
	var building *index.IndexingError
	if errors.As(err, &building) {
		return render.JSON(map[string]any{
			"index_state":     "indexing",
			"elapsed_seconds": building.ElapsedSeconds(),
			"message":         "index is still building; retry shortly",
		}), true
	}
	return "", false
}

func (t *toolset) indexed(run func(*index.Index, map[string]any) (string, bool)) func(context.Context, map[string]any) (string, bool) {
	return func(ctx context.Context, args map[string]any) (string, bool) {
		idx, err := t.src.Wait(ctx)
		if err != nil {
			if text, ok := indexingPayload(err); ok {
				return text, false
			}
			return err.Error(), true
		}
		return run(idx, args)
	}
}

func (t *toolset) indexedCtx(run func(context.Context, *index.Index, map[string]any) (string, bool)) func(context.Context, map[string]any) (string, bool) {
	return func(ctx context.Context, args map[string]any) (string, bool) {
		idx, err := t.src.Wait(ctx)
		if err != nil {
			if text, ok := indexingPayload(err); ok {
				return text, false
			}
			return err.Error(), true
		}
		return run(ctx, idx, args)
	}
}

func (t *toolset) projectInfo() mcp.Tool {
	return mcp.Tool{
		Name:        "project_info",
		Annotations: readOnlyAnnotations(),
		Description: "Index readiness, module path, package/symbol/xref counts, load errors, and available tools.",
		InputSchema: object(nil),
		Run: func(context.Context, map[string]any) (string, bool) {
			type info struct {
				index.Status
				Tools         int    `json:"tools"`
				Summaries     bool   `json:"summaries_enabled"`
				DBPath        string `json:"db_path"`
				SummaryDBPath string `json:"summary_db_path"`
				PromptVersion string `json:"summary_prompt_version,omitempty"`
			}
			out := info{Status: t.src.Status(), Tools: t.count, Summaries: t.summaries != nil}
			if t.summaries != nil {
				out.DBPath = t.summaries.Path()
				out.SummaryDBPath = t.summaries.SummaryPath()
				out.PromptVersion = cache.PromptVersion
			}
			return render.JSON(out), false
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

// readOnlyAnnotations marks a compiler-exact query that never mutates local
// state or reaches the network.
func readOnlyAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
}

// mutatingAnnotations marks a tool that writes derived state (summaries or the
// index) but does not destroy user data.
func mutatingAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
}

// execAnnotations marks a tool that runs local build/test commands. Re-running
// it is not idempotent in the MCP sense because it observes and executes
// repository state.
func execAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: false}
}

func propString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// propStringOrArray documents a parameter that accepts either one string or a
// JSON array of strings. MCP clients validate against this shape before the
// handler normalizes both forms.
func propStringOrArray(desc string) map[string]any {
	return map[string]any{
		"type":        []string{"string", "array"},
		"items":       map[string]any{"type": "string"},
		"description": desc,
	}
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

func strListArg(args map[string]any, key string) []string {
	switch value := args[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) functionBody() mcp.Tool {
	return mcp.Tool{
		Name:        "get_function_body",
		Annotations: readOnlyAnnotations(),
		Description: "Exact source, cyclomatic complexity, and LOC for functions, methods, and type/struct/interface declarations. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands; use symbol_ids to fetch up to 10 bodies in one call and include_doc to keep leading doc comments.",
		InputSchema: object(map[string]any{
			"symbol_id":   propString("Function, method, or type declaration ID (single-symbol mode)"),
			"symbol_ids":  map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string"}, "description": "Batch symbol IDs (max 10, character-budgeted)"},
			"include_doc": propBool("Include the leading doc comment (default: false)"),
			"max_chars":   propInt("Batch output budget in characters (default 40000)"),
		}),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			id := symbolArg(args)
			ids, err := stringSliceAliasArg(args, "symbol_ids", "ids")
			if err != nil {
				return err.Error(), true
			}
			if id == "" && len(ids) == 0 {
				return "provide 'symbol_id' or 'symbol_ids'", true
			}
			includeDoc := boolArg(args, "include_doc", false)
			if id != "" {
				text, err := t.singleFunctionBody(ctx, idx, id, includeDoc)
				if err != nil {
					return functionBodyError(id, err), true
				}
				return text, false
			}
			if len(ids) > 10 {
				return "symbol_ids exceeds the maximum of 10", true
			}
			return t.batchFunctionBodies(ctx, idx, ids, includeDoc, intArg(args, "max_chars", 40000))
		}),
	}
}

func functionBodyError(_ string, err error) string {
	return symbolErrorPayload(err)
}

func (t *toolset) singleFunctionBody(ctx context.Context, idx *index.Index, id string, includeDoc bool) (string, error) {
	sym, err := idx.SingleSymbol(id)
	if err != nil {
		return "", err
	}
	lines, start, end, err := t.functionBodyLines(idx, sym, includeDoc)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if state := t.src.Status().IndexState; state != index.StateReady {
		fmt.Fprintf(&b, "// index_state: %s\n", state)
	}
	if canonical := sym.CanonicalID(); canonical != "" {
		fmt.Fprintf(&b, "// canonical_id: %s\n", canonical)
	}
	if summary, state, depsChanged := t.enrich(ctx, sym); len(summary) > 0 {
		fmt.Fprintf(&b, "// sync_state: %s deps_changed=%v\n// summary: %s\n", state, depsChanged, summary)
	}
	fmt.Fprintf(&b, "// %s (%s:%d-%d | LOC=%d | cyclomatic=%d)\n",
		sym.ID, sym.File, sym.Line, sym.EndLine, sym.LOC, sym.Cyclomatic)
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
	}
	return b.String(), nil
}

type functionBodyEntry struct {
	SymbolID    string          `json:"symbol_id"`
	CanonicalID string          `json:"canonical_id,omitempty"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	File        string          `json:"file"`
	StartLine   int             `json:"start_line"`
	EndLine     int             `json:"end_line"`
	LOC         int             `json:"loc"`
	Cyclomatic  int             `json:"cyclomatic"`
	Summary     json.RawMessage `json:"summary,omitempty"`
	SyncState   string          `json:"sync_state,omitempty"`
	DepsChanged bool            `json:"deps_changed,omitempty"`
	Source      string          `json:"source"`
}

func (t *toolset) batchFunctionBodies(ctx context.Context, idx *index.Index, ids []string, includeDoc bool, maxChars int) (string, bool) {
	bodies := make([]functionBodyEntry, 0, len(ids))
	truncated := make([]string, 0)
	used := 0
	for i, id := range ids {
		sym, err := idx.SingleSymbol(id)
		if err != nil {
			return functionBodyError(id, err), true
		}
		lines, start, end, err := t.functionBodyLines(idx, sym, includeDoc)
		if err != nil {
			return functionBodyError(id, err), true
		}
		var source strings.Builder
		for line := start; line < end; line++ {
			fmt.Fprintf(&source, "%4d | %s\n", line+1, lines[line])
		}
		entry := functionBodyEntry{
			SymbolID: sym.ID, CanonicalID: sym.CanonicalID(), Name: sym.Name, Kind: sym.Kind,
			File: sym.File, StartLine: sym.Line, EndLine: sym.EndLine,
			LOC: sym.LOC, Cyclomatic: sym.Cyclomatic, Source: source.String(),
		}
		if summary, state, depsChanged := t.enrich(ctx, sym); len(summary) > 0 {
			entry.Summary = summary
			entry.SyncState = string(state)
			entry.DepsChanged = depsChanged
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return err.Error(), true
		}
		if len(bodies) > 0 && used+len(raw) > maxChars {
			truncated = append(truncated, ids[i:]...)
			break
		}
		bodies = append(bodies, entry)
		used += len(raw)
	}
	out := struct {
		Bodies       []functionBodyEntry `json:"bodies"`
		TruncatedIDs []string            `json:"truncated_ids"`
		MaxChars     int                 `json:"max_chars"`
		IndexState   index.State         `json:"index_state"`
	}{
		Bodies: bodies, TruncatedIDs: truncated, MaxChars: maxChars,
		IndexState: t.src.Status().IndexState,
	}
	return render.JSON(out), false
}

func (t *toolset) functionBodyLines(idx *index.Index, sym *index.Symbol, includeDoc bool) ([]string, int, int, error) {
	if sym.EndLine < sym.Line {
		return nil, 0, 0, fmt.Errorf("symbol has no source body: %s", sym.ID)
	}
	raw, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.File)))
	if err != nil {
		return nil, 0, 0, err
	}
	lines := strings.Split(string(raw), "\n")
	start := max(sym.Line-1, 0)
	end := min(sym.EndLine, len(lines))
	if includeDoc {
		if summary := idx.Summary.Get(sym.CanonicalID()); summary != nil && summary.DocStartLine > 0 {
			if docStart := summary.DocStartLine - 1; docStart < start {
				start = max(docStart, 0)
			}
		}
	}
	if start >= end {
		return nil, 0, 0, errors.New("source range is stale; reindex and retry")
	}
	return lines, start, end, nil
}

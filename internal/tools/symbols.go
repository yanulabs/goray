package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) lookupSymbol() mcp.Tool {
	return mcp.Tool{
		Name:        "lookup_symbol",
		Description: "Find symbols by exact ID, exact name, or fuzzy substring. Returns IDs for downstream tools.",
		InputSchema: object(map[string]any{
			"query": propString("Symbol name, ID, or substring"),
			"kind":  propEnum("Optional symbol kind", "func", "method", "interface", "struct", "type", "var", "const"),
			"limit": propInt("Max results (default: 25)"),
		}, "query"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			symbols := idx.Resolve(strArg(args, "query", ""), strArg(args, "kind", ""), intArg(args, "limit", 25))
			type hit struct {
				*index.Symbol
				XRefCount   int             `json:"xref_count"`
				CanonicalID string          `json:"canonical_id,omitempty"`
				Summary     json.RawMessage `json:"summary,omitempty"`
				SyncState   string          `json:"sync_state,omitempty"`
				DepsChanged bool            `json:"deps_changed,omitempty"`
				IndexState  index.State     `json:"index_state"`
			}
			out := make([]hit, 0, len(symbols))
			for _, sym := range symbols {
				summary, state, depsChanged := t.enrich(ctx, sym)
				out = append(out, hit{
					Symbol: sym, XRefCount: len(idx.XRefs[sym.ID]), CanonicalID: sym.CanonicalID(),
					Summary: summary, SyncState: string(state), DepsChanged: depsChanged,
					IndexState: t.src.Status().IndexState,
				})
			}
			return render.JSON(out), false
		}),
	}
}

func (t *toolset) apiSurface() mcp.Tool {
	return mcp.Tool{
		Name:        "get_api_surface",
		Description: "All exported symbols of a package or module root, grouped by kind.",
		InputSchema: object(map[string]any{
			"package": propString("Relative package path (e.g. modules/payments) or full import path"),
		}, "package"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			arg := strArg(args, "package", "")
			pkg, ok := idx.ResolvePackage(arg)
			if !ok {
				return "package not found: " + arg, true
			}

			type item struct {
				ID          string          `json:"id"`
				Name        string          `json:"name"`
				Signature   string          `json:"signature"`
				LOC         string          `json:"loc"`
				XRefs       int             `json:"xrefs"`
				CanonicalID string          `json:"canonical_id,omitempty"`
				Summary     json.RawMessage `json:"summary,omitempty"`
				SyncState   string          `json:"sync_state,omitempty"`
				DepsChanged bool            `json:"deps_changed,omitempty"`
			}
			grouped := map[string][]item{}
			for _, sym := range idx.Symbols {
				if sym.PkgPath != pkg || !sym.Exported {
					continue
				}
				name := sym.Name
				if sym.RecvType != "" {
					name = "(" + sym.RecvType + ")." + sym.Name
				}
				summary, state, depsChanged := t.enrich(ctx, sym)
				grouped[sym.Kind] = append(grouped[sym.Kind], item{
					ID: sym.ID, Name: name, Signature: sym.Signature, CanonicalID: sym.CanonicalID(),
					LOC: fmt.Sprintf("%s:%d", sym.File, sym.Line), XRefs: len(idx.XRefs[sym.ID]),
					Summary: summary, SyncState: string(state), DepsChanged: depsChanged,
				})
			}
			for kind := range grouped {
				sort.Slice(grouped[kind], func(i, j int) bool { return grouped[kind][i].Name < grouped[kind][j].Name })
			}
			return render.JSON(map[string]any{
				"package":     pkg,
				"exported":    idx.Packages[pkg].Exported,
				"symbols":     grouped,
				"index_state": t.src.Status().IndexState,
			}), false
		}),
	}
}

func (t *toolset) references() mcp.Tool {
	return mcp.Tool{
		Name:        "get_references",
		Description: "All type-checked references to a symbol, including caller function and file:line.",
		InputSchema: object(map[string]any{
			"symbol": propString("Symbol ID or name"),
			"limit":  propInt("Max references (default: 100)"),
		}, "symbol"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(strArg(args, "symbol", ""))
			if err != nil {
				return err.Error(), true
			}
			all := idx.XRefs[sym.ID]
			limit := intArg(args, "limit", 100)
			refs := all
			truncated := false
			if len(refs) > limit {
				refs = refs[:limit]
				truncated = true
			}
			summary, state, depsChanged := t.enrich(ctx, sym)
			return render.JSON(map[string]any{
				"symbol":       sym,
				"canonical_id": sym.CanonicalID(),
				"total":        len(all),
				"truncated":    truncated,
				"refs":         nonNil(refs),
				"summary":      summary,
				"sync_state":   state,
				"deps_changed": depsChanged,
				"index_state":  t.src.Status().IndexState,
			}), false
		}),
	}
}

func (t *toolset) callGraph() mcp.Tool {
	return mcp.Tool{
		Name:        "get_call_graph",
		Description: "BFS call graph (callers, callees, or both) from a function or method.",
		InputSchema: object(map[string]any{
			"symbol":    propString("Function/method ID or name"),
			"direction": propEnum("Default: both", "callers", "callees", "both"),
			"depth":     propInt("Max depth (default: 2)"),
			"limit":     propInt("Max nodes (default: 80)"),
		}, "symbol"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(strArg(args, "symbol", ""))
			if err != nil {
				return err.Error(), true
			}
			graph := idx.CallGraph(sym.ID, strArg(args, "direction", "both"), intArg(args, "depth", 2), intArg(args, "limit", 80))
			out := struct {
				index.Graph
				IndexState index.State `json:"index_state"`
			}{Graph: graph, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		}),
	}
}

func (t *toolset) implementations() mcp.Tool {
	return mcp.Tool{
		Name:        "find_implementations",
		Description: "Compiler-exact go/types.Implements lookup: interface to types, or type to interfaces.",
		InputSchema: object(map[string]any{
			"symbol": propString("Interface or concrete type ID/name"),
		}, "symbol"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(strArg(args, "symbol", ""))
			if err != nil {
				return err.Error(), true
			}
			if sym.Kind == "interface" {
				return render.JSON(map[string]any{
					"interface":       sym,
					"implementations": nonNil(idx.IfaceImpls[sym.ID]),
					"index_state":     t.src.Status().IndexState,
				}), false
			}
			return render.JSON(map[string]any{
				"type":        sym,
				"interfaces":  nonNil(idx.TypeImpls[sym.ID]),
				"index_state": t.src.Status().IndexState,
			}), false
		}),
	}
}

func (t *toolset) deadCode() mcp.Tool {
	return mcp.Tool{
		Name:        "get_dead_code",
		Description: "Zero-reference functions/methods, excluding main, init, hooks, and interface methods.",
		InputSchema: object(map[string]any{
			"package_prefix":   propString("Optional relative package prefix"),
			"include_exported": propBool("Include exported symbols (default: true)"),
			"limit":            propInt("Max results (default: 100)"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			dead := idx.DeadFunctions(
				strings.TrimPrefix(strArg(args, "package_prefix", ""), "./"),
				boolArg(args, "include_exported", true),
			)
			total := len(dead)
			if limit := intArg(args, "limit", 100); len(dead) > limit {
				dead = dead[:limit]
			}
			type candidate struct {
				*index.Symbol
				CanonicalID string `json:"canonical_id,omitempty"`
			}
			items := make([]candidate, 0, len(dead))
			for _, sym := range dead {
				items = append(items, candidate{Symbol: sym, CanonicalID: sym.CanonicalID()})
			}
			return render.JSON(map[string]any{
				"total_candidates": total,
				"candidates":       items,
				"index_state":      t.src.Status().IndexState,
			}), false
		}),
	}
}

func (t *toolset) functionBody() mcp.Tool {
	return mcp.Tool{
		Name:        "get_function_body",
		Description: "Exact source, cyclomatic complexity, and LOC of a function or method.",
		InputSchema: object(map[string]any{
			"symbol": propString("Function/method ID or name"),
		}, "symbol"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(strArg(args, "symbol", ""))
			if err != nil {
				return err.Error(), true
			}
			if sym.EndLine < sym.Line {
				return "symbol has no source body: " + sym.ID, true
			}
			raw, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.File)))
			if err != nil {
				return err.Error(), true
			}
			lines := strings.Split(string(raw), "\n")
			start := max(sym.Line-1, 0)
			end := min(sym.EndLine, len(lines))
			if start >= end {
				return "source range is stale; reindex and retry", true
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
			return b.String(), false
		}),
	}
}

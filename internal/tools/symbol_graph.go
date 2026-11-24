package tools

import (
	"context"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) references() mcp.Tool {
	return mcp.Tool{
		Name:        "get_references",
		Description: "All type-checked references to a symbol, including caller function and file:line. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Symbol ID, full Go identifier, or repository-relative shorthand"),
			"limit":     propInt("Max references (default: 100)"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
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
		Description: "BFS call graph (callers, callees, or both) from a function or method. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id":                  propString("Function/method ID, full Go identifier, or repository-relative shorthand"),
			"direction":                  propEnum("Default: both", "callers", "callees", "both"),
			"depth":                      propInt("Max depth (default: 2)"),
			"limit":                      propInt("Max nodes (default: 80)"),
			"include_interface_dispatch": propBool("Include in-module interface dispatch edges (default: true)"),
		}, "symbol_id"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			graph := idx.CallGraphWithOptions(
				sym.ID,
				strArg(args, "direction", "both"),
				intArg(args, "depth", 2),
				intArg(args, "limit", 80),
				boolArg(args, "include_interface_dispatch", true),
			)
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
		Description: "Compiler-exact go/types.Implements lookup: interface to types, or type to interfaces. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Interface or concrete type ID, full Go identifier, or repository-relative shorthand"),
		}, "symbol_id"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
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

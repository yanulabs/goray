package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) references() mcp.Tool {
	return mcp.Tool{
		Name:        "get_references",
		Annotations: readOnlyAnnotations(),
		Description: "All type-checked references to a symbol, including caller function and file:line. Supports pagination plus test, module, and package filters. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id":     propString("Symbol ID, full Go identifier, or repository-relative shorthand"),
			"limit":         propInt("Max references per page (default: 100)"),
			"offset":        propInt("Pagination offset (default: 0)"),
			"include_tests": propBool("Include *_test.go references (default: true)"),
			"module":        propString("Optional module filter for the referencing package"),
			"package":       propString("Optional package filter for the referencing package"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			moduleFilter := ""
			if raw := moduleArg(args); raw != "" {
				resolved, ok := idx.ResolveModule(raw)
				if !ok {
					return fmt.Sprintf("module %q not found; available: %s", raw, strings.Join(idx.SortedModuleNames(), ", ")), true
				}
				moduleFilter = resolved
			}
			packageFilter := ""
			if raw := packageArg(args); raw != "" {
				resolved, ok := idx.ResolvePackage(raw)
				if !ok {
					return "package not found: " + raw, true
				}
				packageFilter = resolved
			}
			includeTests := boolArg(args, "include_tests", true)

			all := idx.XRefs[sym.ID]
			filtered := make([]index.XRef, 0, len(all))
			for _, ref := range all {
				if !includeTests && (ref.Test || strings.HasSuffix(ref.File, "_test.go")) {
					continue
				}
				if moduleFilter != "" && ref.FromMod != moduleFilter {
					continue
				}
				if packageFilter != "" && ref.FromPkg != packageFilter {
					continue
				}
				filtered = append(filtered, ref)
			}

			offset := nonNegativeIntArg(args, "offset", 0)
			if offset > len(filtered) {
				offset = len(filtered)
			}
			limit := intArg(args, "limit", 100)
			refs := filtered[offset:]
			truncated := false
			nextOffset := 0
			if len(refs) > limit {
				refs = refs[:limit]
				truncated = true
				nextOffset = offset + len(refs)
			}
			summary, state, depsChanged := t.enrich(ctx, sym)
			out := map[string]any{
				"symbol":       sym,
				"canonical_id": sym.CanonicalID(),
				"total":        len(filtered),
				"offset":       offset,
				"returned":     len(refs),
				"truncated":    truncated,
				"refs":         nonNil(refs),
				"summary":      summary,
				"sync_state":   state,
				"deps_changed": depsChanged,
				"index_state":  t.src.Status().IndexState,
			}
			if truncated {
				out["next_offset"] = nextOffset
			}
			return render.JSON(out), false
		}),
	}
}

func (t *toolset) callGraph() mcp.Tool {
	return mcp.Tool{
		Name:        "get_call_graph",
		Annotations: readOnlyAnnotations(),
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
		Annotations: readOnlyAnnotations(),
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

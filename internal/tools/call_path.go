package tools

import (
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) findCallPath() mcp.Tool {
	return mcp.Tool{
		Name:        "find_call_path",
		Annotations: readOnlyAnnotations(),
		Description: "Deterministic shortest call chain(s) between two functions or methods. Follows direct, in-module interface-dispatch, and func_value edges, reports file:line hops, and returns found:false with a reverse-path hint when unreachable.",
		InputSchema: object(map[string]any{
			"from":                       propString("Source function/method ID, full Go identifier, or repository-relative shorthand"),
			"to":                         propString("Target function/method ID, full Go identifier, or repository-relative shorthand"),
			"max_depth":                  propInt("Max hops (default: 8, max: 20)"),
			"max_paths":                  propInt("Max shortest paths to return (default: 3, max: 10)"),
			"include_interface_dispatch": propBool("Include in-module interface dispatch edges (default: true)"),
		}, "from", "to"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			from := strArg(args, "from", "")
			to := strArg(args, "to", "")
			if from == "" || to == "" {
				return "both 'from' and 'to' are required", true
			}
			fromSym, err := idx.SingleSymbol(from)
			if err != nil {
				return symbolErrorPayload(err), true
			}
			if !callPathCallable(fromSym) {
				return callPathKindErrorPayload("from", fromSym), true
			}
			toSym, err := idx.SingleSymbol(to)
			if err != nil {
				return symbolErrorPayload(err), true
			}
			if !callPathCallable(toSym) {
				return callPathKindErrorPayload("to", toSym), true
			}

			result, err := idx.FindCallPath(
				fromSym.ID,
				toSym.ID,
				intArg(args, "max_depth", 8),
				intArg(args, "max_paths", 3),
				boolArg(args, "include_interface_dispatch", true),
			)
			if err != nil {
				return err.Error(), true
			}
			out := struct {
				index.CallPathResult
				IndexState index.State `json:"index_state"`
			}{CallPathResult: result, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		}),
	}
}

func callPathCallable(sym *index.Symbol) bool {
	return sym != nil && (sym.Kind == "func" || sym.Kind == "method")
}

func callPathKindErrorPayload(role string, sym *index.Symbol) string {
	return render.JSON(map[string]any{
		"error": "not_callable_symbol",
		"role":  role,
		"input": sym.ID,
		"kind":  sym.Kind,
		"hint":  "find_call_path requires function or method endpoints; use get_call_graph or find_implementations for other symbol kinds",
	})
}

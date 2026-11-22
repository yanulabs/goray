package tools

import (
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) impactAnalysis() mcp.Tool {
	return mcp.Tool{
		Name:        "get_impact_analysis",
		Description: "Blast-radius analysis for a symbol or package: impacted callers, files, modules, and test packages. Prefers symbol_id; set package for package-scoped impact.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Target symbol ID, full Go identifier, or repository-relative shorthand"),
			"package":   propString("Target relative package path (used if symbol_id is empty)"),
			"depth":     propInt("Max traversal depth (default: 3)"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			depth := intArg(args, "depth", 3)
			symArg := symbolArg(args)
			pkgArg := strings.TrimPrefix(packageArg(args), "./")

			switch {
			case symArg != "":
				sym, err := idx.SingleSymbol(symArg)
				if err != nil {
					return symbolErrorPayload(err), true
				}
				out := struct {
					index.Impact
					IndexState index.State `json:"index_state"`
				}{Impact: idx.ImpactBySymbol(sym, depth), IndexState: t.src.Status().IndexState}
				return render.JSON(out), false
			case pkgArg != "":
				pkg, ok := idx.ResolvePackage(pkgArg)
				if !ok {
					return "package or module not found: " + pkgArg, true
				}
				out := struct {
					index.Impact
					IndexState index.State `json:"index_state"`
				}{Impact: idx.ImpactByPackage(pkg, depth), IndexState: t.src.Status().IndexState}
				return render.JSON(out), false
			default:
				return "specify 'symbol_id' or 'package'", true
			}
		}),
	}
}

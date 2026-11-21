package tools

import (
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
)

func (t *toolset) impactAnalysis() mcp.Tool {
	return mcp.Tool{
		Name:        "get_impact_analysis",
		Description: "Blast-radius analysis for a symbol or package: impacted callers, files, modules, and test packages.",
		InputSchema: object(map[string]any{
			"symbol":  propString("Target symbol ID or name"),
			"package": propString("Target relative package path (used if symbol is empty)"),
			"depth":   propInt("Max traversal depth (default: 3)"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			depth := intArg(args, "depth", 3)
			symArg := strArg(args, "symbol", "")
			pkgArg := strings.TrimPrefix(strArg(args, "package", ""), "./")

			switch {
			case symArg != "":
				sym, err := idx.SingleSymbol(symArg)
				if err != nil {
					return err.Error(), true
				}
				return toJSON(idx.ImpactBySymbol(sym, depth)), false
			case pkgArg != "":
				pkg, ok := idx.ResolvePackage(pkgArg)
				if !ok {
					return "package or module not found: " + pkgArg, true
				}
				return toJSON(idx.ImpactByPackage(pkg, depth)), false
			default:
				return "specify 'symbol' or 'package'", true
			}
		}),
	}
}

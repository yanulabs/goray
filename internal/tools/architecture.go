package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

func (t *toolset) codebaseMap() mcp.Tool {
	return mcp.Tool{
		Name:        "get_codebase_map",
		Description: "High-level architecture map: module scorecard, cross-module dependency graph, symbol coupling, and bloated files.",
		InputSchema: object(map[string]any{
			"format": propEnum("Output format (default: markdown)", "markdown", "json", "dot"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			switch strArg(args, "format", "markdown") {
			case "dot":
				return render.CodebaseMapDOT(idx), false
			case "json":
				return toJSON(codebaseData(idx)), false
			default:
				return render.CodebaseMapMarkdown(idx), false
			}
		}),
	}
}

func (t *toolset) moduleOverview() mcp.Tool {
	return mcp.Tool{
		Name:        "get_module_overview",
		Description: "Deep dive on one module: subpackages, incoming/outgoing symbol dependencies, .Ports() calls, large files, and exported root API.",
		InputSchema: object(map[string]any{
			"module": propString("Module name (e.g. payments, bookinggw)"),
		}, "module"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			name := strArg(args, "module", "")
			summary, ok := idx.Modules[name]
			if !ok {
				return fmt.Sprintf("module %q not found; available: %s", name, strings.Join(idx.SortedModuleNames(), ", ")), true
			}
			return render.ModuleOverview(idx, summary), false
		}),
	}
}

func (t *toolset) dependencyGraph() mcp.Tool {
	return mcp.Tool{
		Name:        "get_dependency_graph",
		Description: "Package dependency graph with optional BFS subgraph. Without a root, ranks packages by fan-in/fan-out.",
		InputSchema: object(map[string]any{
			"root":      propString("Optional package path or module name"),
			"direction": propEnum("Traversal direction (default: out)", "out", "in", "both"),
			"depth":     propInt("BFS max depth (default: 2)"),
			"format":    propEnum("Output format (default: json)", "json", "mermaid", "dot"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			format := strArg(args, "format", "json")
			root := strings.TrimPrefix(strArg(args, "root", ""), "./")
			if root == "" {
				switch format {
				case "dot":
					return render.CodebaseMapDOT(idx), false
				case "mermaid":
					return render.ModuleMermaid(idx), false
				default:
					return toJSON(idx.FanStats(40)), false
				}
			}

			pkg, ok := idx.ResolvePackage(root)
			if !ok {
				return "package or module not found: " + root, true
			}
			graph := idx.DependencyGraph(pkg, strArg(args, "direction", "out"), intArg(args, "depth", 2))
			switch format {
			case "mermaid":
				return render.DependencyMermaid(graph), false
			case "dot":
				return render.DependencyDOT(graph), false
			default:
				return toJSON(graph), false
			}
		}),
	}
}

func codebaseData(idx *index.Index) map[string]any {
	complex := make([]*index.Symbol, 0)
	for _, sym := range idx.Symbols {
		if sym.Cyclomatic >= 10 {
			complex = append(complex, sym)
		}
	}
	sort.Slice(complex, func(i, j int) bool {
		if complex[i].Cyclomatic != complex[j].Cyclomatic {
			return complex[i].Cyclomatic > complex[j].Cyclomatic
		}
		return complex[i].ID < complex[j].ID
	})
	if len(complex) > 20 {
		complex = complex[:20]
	}

	large := idx.LargeFiles
	if len(large) > 25 {
		large = large[:25]
	}
	return map[string]any{
		"module_path": idx.ModulePath,
		"packages":    len(idx.Packages),
		"symbols":     len(idx.Symbols),
		"modules":     idx.ModulesByLOC(),
		"large_files": large,
		"top_complex": complex,
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

func (t *toolset) codebaseMap() mcp.Tool {
	return mcp.Tool{
		Name:        "get_codebase_map",
		Description: "High-level architecture map: module scorecard (explicit modules or standard layout groups), cross-module dependency graph, symbol coupling, and bloated files.",
		InputSchema: object(map[string]any{
			"format": propEnum("Output format (default: markdown)", t.formats.CodebaseMapFormats()...),
		}),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			format := strArg(args, "format", "markdown")
			out, ok := t.formats.RenderCodebaseMap(format, idx)
			if !ok {
				return "unsupported format: " + format, true
			}
			if t.summaries == nil || (format != "json" && format != "markdown") {
				return out, false
			}
			summaries := map[string]cachedModule{}
			for _, name := range idx.SortedModuleNames() {
				cached, state, depsChanged, err := t.summaries.Enrich(ctx, "mod:"+name)
				if err == nil && len(cached) > 0 {
					summaries[name] = cachedModule{summary: cached, state: string(state), depsChanged: depsChanged}
				}
			}
			if len(summaries) == 0 {
				return out, false
			}
			if format == "json" {
				var view map[string]any
				if json.Unmarshal([]byte(out), &view) != nil {
					return out, false
				}
				modules, _ := view["modules"].([]any)
				for _, item := range modules {
					module, _ := item.(map[string]any)
					name, _ := module["name"].(string)
					if cached, ok := summaries[name]; ok {
						module["summary"] = cached.summary
						module["sync_state"] = cached.state
						module["deps_changed"] = cached.depsChanged
					}
				}
				return render.JSON(view), false
			}
			var b strings.Builder
			b.WriteString(out)
			b.WriteString("\n## Cached Module Summaries\n")
			for _, name := range idx.SortedModuleNames() {
				cached, ok := summaries[name]
				if !ok {
					continue
				}
				fmt.Fprintf(&b, "\n### `%s` (%s)\n\n```json\n%s\n```\n", name, cached.state, cached.summary)
			}
			return b.String(), false
		}),
	}
}

type cachedModule struct {
	summary     json.RawMessage
	state       string
	depsChanged bool
}

func (t *toolset) moduleOverview() mcp.Tool {
	return mcp.Tool{
		Name:        "get_module_overview",
		Description: "Deep dive on one module: subpackages, incoming/outgoing symbol dependencies, .Ports() calls, large files, and exported root API.",
		InputSchema: object(map[string]any{
			"module": propString("Module name (e.g. payments, internal/index)"),
			"format": propEnum("Output format (default: markdown)", t.formats.ModuleFormats()...),
		}, "module"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			name := strArg(args, "module", "")
			summary, ok := idx.Modules[name]
			if !ok {
				return fmt.Sprintf("module %q not found; available: %s", name, strings.Join(idx.SortedModuleNames(), ", ")), true
			}
			format := strArg(args, "format", "markdown")
			out, ok := t.formats.RenderModule(format, idx, summary)
			if !ok {
				return "unsupported format: " + format, true
			}
			if t.summaries == nil {
				return out, false
			}
			cached, state, depsChanged, err := t.summaries.Enrich(ctx, "mod:"+name)
			if err != nil || len(cached) == 0 {
				return out, false
			}
			if format == "json" {
				var view map[string]any
				if json.Unmarshal([]byte(out), &view) == nil {
					view["summary"] = cached
					view["sync_state"] = state
					view["deps_changed"] = depsChanged
					return render.JSON(view), false
				}
				return out, false
			}
			var b strings.Builder
			b.WriteString(out)
			fmt.Fprintf(&b, "\n## Cached Summary\n\n- Sync state: `%s`\n\n```json\n%s\n```\n", state, cached)
			return b.String(), false
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
			"format":    propEnum("Output format (default: json)", t.formats.GraphFormats()...),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			format := strArg(args, "format", "json")
			root := strings.TrimPrefix(strArg(args, "root", ""), "./")
			if root == "" {
				if format == "json" {
					return render.JSON(idx.FanStats(40)), false
				}
				out, ok := t.formats.RenderCodebaseMap(format, idx)
				if !ok {
					return "unsupported format: " + format, true
				}
				return out, false
			}

			pkg, ok := idx.ResolvePackage(root)
			if !ok {
				return "package or module not found: " + root, true
			}
			graph := idx.DependencyGraph(pkg, strArg(args, "direction", "out"), intArg(args, "depth", 2))
			out, ok := t.formats.RenderGraph(format, graph)
			if !ok {
				return "unsupported format: " + format, true
			}
			return out, false
		}),
	}
}

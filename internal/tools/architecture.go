package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) codebaseMap() mcp.Tool {
	return mcp.Tool{
		Name:        "get_codebase_map",
		Description: "High-level architecture map: module scorecard (explicit modules or standard layout groups), cross-module dependency edges, symbol coupling, and bloated files. Compact by default; set detail=\"full\" for the legacy full projection.",
		InputSchema: object(map[string]any{
			"format":       propEnum("Output format (default: markdown)", t.formats.CodebaseMapFormats()...),
			"detail":       propEnum("Output detail (default: compact)", "compact", "full"),
			"summary_only": propBool("Return only the module scorecard and payload metadata (default: false)"),
			"top_modules":  propInt("Max modules in compact output, sorted by sort_by (default: 15; 0 = all)"),
			"sort_by":      propEnum("Compact module ordering", "prod_loc", "fan_in", "fan_out"),
			"tracked_only": propBool("Exclude git-untracked files and modules (default: false)"),
		}),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			format := strArg(args, "format", "markdown")
			if boolArg(args, "tracked_only", false) {
				idx = idx.TrackedView()
			}
			if strArg(args, "detail", "compact") == "compact" {
				return t.compactCodebaseMap(idx, args)
			}
			if detail := strArg(args, "detail", "compact"); detail != "full" {
				return "invalid detail: " + detail, true
			}
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
		Description: "Deep dive on one module: package rollups, incoming/outgoing dependency edges, .Ports() calls, large files, and top hotspots. Compact by default; generated content is excluded unless include_generated=true.",
		InputSchema: object(map[string]any{
			"module":            propString("Module name (e.g. payments, internal/index) or canonical mod: ID"),
			"format":            propEnum("Output format (default: markdown)", t.formats.ModuleFormats()...),
			"detail":            propEnum("Output detail (default: compact)", "compact", "full"),
			"include_generated": propBool("Include generated symbols in hotspots and omit generated_omitted (default: false)"),
			"tracked_only":      propBool("Exclude git-untracked files (default: false)"),
		}, "module"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			if boolArg(args, "tracked_only", false) {
				idx = idx.TrackedView()
			}
			name, ok := idx.ResolveModule(moduleArg(args))
			if !ok {
				return fmt.Sprintf("module %q not found; available: %s", moduleArg(args), strings.Join(idx.SortedModuleNames(), ", ")), true
			}
			summary := idx.Modules[name]
			detail := strArg(args, "detail", "compact")
			switch detail {
			case "compact":
				return t.compactModuleOverview(idx, summary, args)
			case "full":
			default:
				return "invalid detail: " + detail, true
			}
			format := strArg(args, "format", "markdown")
			out, ok := t.formats.RenderModule(format, idx, summary)
			if !ok {
				return "unsupported format: " + format, true
			}
			includeGenerated := boolArg(args, "include_generated", false)
			if !includeGenerated && (summary.GeneratedFiles > 0 || summary.GeneratedSymbols > 0 || summary.GeneratedLOC > 0) {
				omitted := generatedOmitted{Files: summary.GeneratedFiles, Symbols: summary.GeneratedSymbols, LOC: summary.GeneratedLOC}
				hint := "set include_generated=true to include generated symbols"
				switch format {
				case "json":
					var view map[string]any
					if json.Unmarshal([]byte(out), &view) == nil {
						view["generated_omitted"] = omitted
						view["truncated"] = true
						view["total_count"] = summary.Packages
						view["hint"] = hint
						out = render.JSON(view)
					}
				default:
					out += fmt.Sprintf("\n## Generated\n\n- generated_omitted.files: %d\n- generated_omitted.symbols: %d\n- generated_omitted.loc: %d\n- truncated: true\n- total_count: %d\n- hint: %s\n",
						omitted.Files, omitted.Symbols, omitted.LOC, summary.Packages, hint)
				}
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
		Description: "Package dependency graph with optional BFS subgraph. Without a package, ranks packages by fan-in/fan-out.",
		InputSchema: object(map[string]any{
			"package":   propString("Optional package path or module name"),
			"direction": propEnum("Traversal direction (default: out)", "out", "in", "both"),
			"depth":     propInt("BFS max depth (default: 2)"),
			"format":    propEnum("Output format (default: json)", t.formats.GraphFormats()...),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			format := strArg(args, "format", "json")
			root := strings.TrimPrefix(packageArg(args), "./")
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

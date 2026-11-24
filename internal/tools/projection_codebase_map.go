package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func compactModules(idx *index.Index, sortBy string) []compactModule {
	modules := make([]*index.ModSummary, 0, len(idx.Modules))
	for _, mod := range idx.Modules {
		modules = append(modules, mod)
	}
	sort.Slice(modules, func(i, j int) bool {
		left, right := modules[i], modules[j]
		switch sortBy {
		case "fan_in":
			if len(left.InModules) != len(right.InModules) {
				return len(left.InModules) > len(right.InModules)
			}
		case "fan_out":
			if len(left.OutModules) != len(right.OutModules) {
				return len(left.OutModules) > len(right.OutModules)
			}
		default:
			if left.ProdLOC != right.ProdLOC {
				return left.ProdLOC > right.ProdLOC
			}
		}
		return left.Name < right.Name
	})
	out := make([]compactModule, 0, len(modules))
	for _, mod := range modules {
		out = append(out, compactModuleSummary(mod))
	}
	return out
}

func compactEdges(idx *index.Index) []compactEdge {
	edges := make([]compactEdge, 0)
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		for _, dst := range sortedModuleKeys(mod.OutModules) {
			edges = append(edges, compactEdge{From: name, To: dst, Symbols: len(mod.OutModules[dst])})
		}
	}
	return edges
}

func compactCodebaseResponse(idx *index.Index, detail string, summaryOnly bool, sortBy string, shown []compactModule, totalModules, omittedModules int, state index.State) compactCodebase {
	generatedFiles, generatedSymbols, generatedLOC := generatedTotals(idx)
	return compactCodebase{
		ModulePath: idx.ModulePath, Packages: len(idx.Packages), ModuleCount: len(idx.Modules),
		Symbols: len(idx.Symbols), GeneratedFiles: generatedFiles, GeneratedSymbols: generatedSymbols,
		GeneratedLOC: generatedLOC, Detail: detail, SummaryOnly: summaryOnly, SortBy: sortBy,
		Modules: shown, TotalModules: totalModules, OmittedModules: omittedModules, OmittedCount: omittedModules,
		TotalCount: totalModules, ReturnedCount: len(shown), IndexState: state,
	}
}

func populateCompactCodebaseDetails(out *compactCodebase, idx *index.Index) {
	if out.SummaryOnly {
		return
	}
	out.CrossModuleEdges = compactEdges(idx)
	cycles := idx.ModuleCycles()
	out.ModuleCycles = make([]string, 0, len(cycles))
	for _, cycle := range cycles {
		out.ModuleCycles = append(out.ModuleCycles, strings.Join(cycle, " -> "))
	}
	out.LargeFiles, out.TotalLargeFiles = pageLargeFiles(idx.LargeFiles, false)
	hotspots, total := idx.Hotspots(index.HotspotOptions{Target: "func", SortBy: "risk", Limit: 10})
	out.TotalHotspots = total
	out.TopHotspots = hotspots
}

func finalizeCompactCodebase(out *compactCodebase, summaryOnly bool, topModules, omittedModules int) {
	out.Truncated = omittedModules > 0 || summaryOnly
	if !summaryOnly && (len(out.LargeFiles) < out.TotalLargeFiles || len(out.TopHotspots) < out.TotalHotspots) {
		out.Truncated = true
	}
	out.Hint = codebaseHint(summaryOnly, topModules, omittedModules, len(out.LargeFiles), out.TotalLargeFiles, len(out.TopHotspots), out.TotalHotspots)
}

func (t *toolset) compactCodebaseMap(idx *index.Index, args map[string]any) (string, bool) {
	detail := strArg(args, "detail", "compact")
	if detail != "compact" && detail != "full" {
		return "invalid detail: " + detail, true
	}
	summaryOnly := boolArg(args, "summary_only", false)
	sortBy := strArg(args, "sort_by", "prod_loc")
	switch sortBy {
	case "prod_loc", "fan_in", "fan_out":
	default:
		return "invalid sort_by: " + sortBy, true
	}
	topModules := nonNegativeIntArg(args, "top_modules", 15)
	allModules := compactModules(idx, sortBy)
	totalModules := len(allModules)
	shown, omittedModules := paginateCompactModules(allModules, topModules)
	out := compactCodebaseResponse(idx, detail, summaryOnly, sortBy, shown, totalModules, omittedModules, t.src.Status().IndexState)
	populateCompactCodebaseDetails(&out, idx)
	finalizeCompactCodebase(&out, summaryOnly, topModules, omittedModules)

	format := strArg(args, "format", "markdown")
	switch format {
	case "json":
		return render.JSON(out), false
	case "markdown":
		return compactCodebaseMarkdown(out), false
	default:
		full, ok := t.formats.RenderCodebaseMap(format, idx)
		if !ok {
			return "unsupported format: " + format, true
		}
		return full, false
	}
}

func codebaseHint(summaryOnly bool, topModules, omitted, largeShown, largeTotal int, hotShown, hotTotal int) string {
	hints := make([]string, 0, 3)
	if summaryOnly {
		hints = append(hints, `set summary_only=false to include dependency edges, cycles, large files, and hotspots`)
	}
	if omitted > 0 {
		hints = append(hints, fmt.Sprintf(`increase top_modules (currently %d; 0 = all %d modules) or use detail="full"`, topModules, omitted+topModules))
	}
	if largeShown < largeTotal {
		hints = append(hints, fmt.Sprintf(`increase the large-file window or use detail="full" for all %d large files`, largeTotal))
	}
	if hotShown < hotTotal {
		hints = append(hints, fmt.Sprintf(`call get_hotspots for all %d hotspots`, hotTotal))
	}
	return strings.Join(hints, "; ")
}

func compactCodebaseMarkdown(out compactCodebase) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture Map: `%s`\n\n", out.ModulePath)
	fmt.Fprintf(&b, "- **Packages:** %d | **Modules:** %d | **Symbols:** %d | **Generated files:** %d | **Generated symbols:** %d | **Generated LOC:** %d | **Untracked files:** %d\n\n",
		out.Packages, out.ModuleCount, out.Symbols, out.GeneratedFiles, out.GeneratedSymbols, out.GeneratedLOC, out.UntrackedFiles)
	fmt.Fprintf(&b, "## Module Scorecard (top %d of %d by %s)\n\n", out.ReturnedCount, out.TotalModules, out.SortBy)
	b.WriteString("| Module | Pkgs | Files | Gen files | Gen syms | Gen LOC | Untracked | LOC | Fan-in | Fan-out |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mod := range out.Modules {
		label := mod.Name
		if mod.TestOnly {
			label += " [test-only]"
		}
		if mod.AllUntracked {
			label += " [untracked]"
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			label, mod.Packages, mod.Files, mod.GeneratedFiles, mod.GeneratedSymbols, mod.GeneratedLOC,
			mod.UntrackedFiles, mod.ProdLOC, mod.FanIn, mod.FanOut)
	}
	if out.SummaryOnly {
		writePayloadFooter(&b, out.Truncated, out.TotalCount, out.Hint)
		return b.String()
	}
	b.WriteString("\n## Cross-Module Edges\n\n")
	if len(out.CrossModuleEdges) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, edge := range out.CrossModuleEdges {
			fmt.Fprintf(&b, "- `%s` -> `%s` (%d symbols)\n", edge.From, edge.To, edge.Symbols)
		}
	}
	b.WriteString("\n## Cross-Module Cycles\n\n")
	if len(out.ModuleCycles) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, cycle := range out.ModuleCycles {
			fmt.Fprintf(&b, "- `%s`\n", cycle)
		}
	}
	if len(out.LargeFiles) > 0 {
		b.WriteString("\n## Large Files (non-generated, max 10)\n\n| LOC | File |\n|---:|---|\n")
		for _, file := range out.LargeFiles {
			fmt.Fprintf(&b, "| %d | `%s` |\n", file.LOC, file.Path)
		}
	}
	if len(out.TopHotspots) > 0 {
		b.WriteString("\n## Top Hotspots (max 10)\n\n| Risk | Name | Module | File | LOC | Complexity | Fan-in |\n|---:|---|---|---|---:|---:|---:|\n")
		for _, hotspot := range out.TopHotspots {
			fmt.Fprintf(&b, "| %d | `%s` | `%s` | `%s` | %d | %d | %d |\n",
				hotspot.RiskScore, hotspot.Name, hotspot.Module, hotspot.File, hotspot.LOC, hotspot.Complexity, hotspot.FanIn)
		}
	}
	writePayloadFooter(&b, out.Truncated, out.TotalCount, out.Hint)
	return b.String()
}

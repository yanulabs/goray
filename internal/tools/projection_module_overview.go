package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func compactPackageRollups(idx *index.Index, module string) []compactPackage {
	packages := make([]*index.PkgInfo, 0)
	for _, pkg := range idx.Packages {
		if pkg.ModuleArea == module {
			packages = append(packages, pkg)
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].RelPath < packages[j].RelPath })
	out := make([]compactPackage, 0, len(packages))
	for _, pkg := range packages {
		out = append(out, compactPackage{
			PkgPath: pkg.PkgPath, RelPath: pkg.RelPath, Name: pkg.Name,
			ProdFiles: pkg.ProdFiles, Files: pkg.Files, TestFiles: pkg.TestFiles,
			ProdLOC: pkg.ProdLOC, TestLOC: pkg.TestLOC, Exported: pkg.Exported,
			PortsCalls: pkg.PortsCalls, Goroutines: pkg.Goroutines,
			GeneratedFiles: pkg.GeneratedFiles, GeneratedSymbols: pkg.GeneratedSymbols,
			GeneratedLOC: pkg.GeneratedLOC, UntrackedFiles: pkg.UntrackedFiles,
			TestOnly: pkg.TestOnly,
		})
	}
	return out
}

func compactModuleEdges(mod *index.ModSummary) dependencyEdges {
	out := make([]moduleEdgeCount, 0, len(mod.OutModules))
	for _, name := range sortedModuleKeys(mod.OutModules) {
		out = append(out, moduleEdgeCount{Module: name, Symbols: len(mod.OutModules[name])})
	}
	in := make([]moduleEdgeCount, 0, len(mod.InModules))
	for _, name := range sortedModuleKeys(mod.InModules) {
		in = append(in, moduleEdgeCount{Module: name, Symbols: len(mod.InModules[name])})
	}
	return dependencyEdges{Out: out, In: in}
}

func compactModuleResponse(idx *index.Index, mod *index.ModSummary, includeGenerated bool, state index.State) compactModuleOverview {
	packages := compactPackageRollups(idx, mod.Name)
	hotspots, totalHotspots := idx.Hotspots(index.HotspotOptions{
		Target: "func", SortBy: "risk", Limit: 10, Module: mod.Name,
		IncludeGenerated: includeGenerated,
	})
	large, totalLarge := pageLargeFiles(mod.LargeFiles, includeGenerated)
	return compactModuleOverview{
		Module: compactModuleSummary(mod), PackageRollups: packages, Subpackages: packages, TotalPackages: len(packages),
		DependencyEdges: compactModuleEdges(mod), TopHotspots: hotspots, TotalHotspots: totalHotspots,
		LargeFiles: large, TotalLargeFiles: totalLarge, IncludeGenerated: includeGenerated,
		TotalCount: len(packages), ReturnedCount: len(packages), IndexState: state,
	}
}

func finalizeCompactModuleOverview(out *compactModuleOverview, mod *index.ModSummary) {
	if !out.IncludeGenerated && (mod.GeneratedFiles > 0 || mod.GeneratedSymbols > 0 || mod.GeneratedLOC > 0) {
		out.GeneratedOmitted = &generatedOmitted{Files: mod.GeneratedFiles, Symbols: mod.GeneratedSymbols, LOC: mod.GeneratedLOC}
	}
	out.Truncated = out.GeneratedOmitted != nil || len(out.LargeFiles) < out.TotalLargeFiles || len(out.TopHotspots) < out.TotalHotspots
	out.Hint = moduleOverviewHint(*out, mod)
}

func moduleOverviewHint(out compactModuleOverview, mod *index.ModSummary) string {
	hints := make([]string, 0, 3)
	if out.GeneratedOmitted != nil {
		hints = append(hints, fmt.Sprintf(`set include_generated=true to include %d generated symbols and %d generated LOC`, mod.GeneratedSymbols, mod.GeneratedLOC))
	}
	if len(out.LargeFiles) < out.TotalLargeFiles {
		hints = append(hints, fmt.Sprintf("large files are capped at 10 of %d", out.TotalLargeFiles))
	}
	if len(out.TopHotspots) < out.TotalHotspots {
		hints = append(hints, fmt.Sprintf("call get_hotspots with module=%q for all %d hotspots", mod.Name, out.TotalHotspots))
	}
	return strings.Join(hints, "; ")
}

func (t *toolset) compactModuleOverview(ctx context.Context, idx *index.Index, mod *index.ModSummary, args map[string]any) (string, bool) {
	includeGenerated := boolArg(args, "include_generated", false)
	out := compactModuleResponse(idx, mod, includeGenerated, t.src.Status().IndexState)
	if t.summaries != nil {
		cached, state, depsChanged, err := t.summaries.Enrich(ctx, "mod:"+mod.Name)
		if err == nil && len(cached) > 0 {
			out.Summary = cached
			out.SyncState = string(state)
			out.DepsChanged = depsChanged
		}
	}
	finalizeCompactModuleOverview(&out, mod)

	format := strArg(args, "format", "markdown")
	switch format {
	case "json":
		return render.JSON(out), false
	case "markdown":
		return compactModuleMarkdown(out), false
	default:
		full, ok := t.formats.RenderModule(format, idx, mod)
		if !ok {
			return "unsupported format: " + format, true
		}
		return full, false
	}
}

func compactModuleMarkdown(out compactModuleOverview) string {
	mod := out.Module
	var b strings.Builder
	fmt.Fprintf(&b, "# Module `%s`\n\n", mod.Name)
	if mod.TestOnly {
		fmt.Fprintf(&b, "- Classification: **test-only** (%d test LOC)\n", mod.TestLOC)
	}
	fmt.Fprintf(&b, "- Prod LOC: %d | Test LOC: %d | Packages: %d | Files: %d | Generated: %d files / %d symbols / %d LOC\n",
		mod.ProdLOC, mod.TestLOC, mod.Packages, mod.Files, mod.GeneratedFiles, mod.GeneratedSymbols, mod.GeneratedLOC)
	fmt.Fprintf(&b, "- Root Exported: %d | `.Ports()` calls: %d | Goroutines: %d | Fan-in: %d | Fan-out: %d\n\n",
		mod.RootExported, mod.PortsCalls, mod.Goroutines, mod.FanIn, mod.FanOut)
	b.WriteString("## Package Rollups\n| RelPath | Prod LOC | Test LOC | Exported | `.Ports()` | Gen files | Gen syms | Gen LOC |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, pkg := range out.PackageRollups {
		label := pkg.RelPath
		if pkg.TestOnly {
			label += " [test-only]"
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %d |\n",
			label, pkg.ProdLOC, pkg.TestLOC, pkg.Exported, pkg.PortsCalls,
			pkg.GeneratedFiles, pkg.GeneratedSymbols, pkg.GeneratedLOC)
	}
	b.WriteString("\n## Dependency Edges\n")
	if len(out.DependencyEdges.Out) == 0 && len(out.DependencyEdges.In) == 0 {
		b.WriteString("- None\n")
	}
	if len(out.DependencyEdges.Out) > 0 {
		b.WriteString("- Outgoing:")
		for _, edge := range out.DependencyEdges.Out {
			fmt.Fprintf(&b, " `%s` (%d)", edge.Module, edge.Symbols)
		}
		b.WriteString("\n")
	}
	if len(out.DependencyEdges.In) > 0 {
		b.WriteString("- Incoming:")
		for _, edge := range out.DependencyEdges.In {
			fmt.Fprintf(&b, " `%s` (%d)", edge.Module, edge.Symbols)
		}
		b.WriteString("\n")
	}
	if len(out.LargeFiles) > 0 {
		b.WriteString("\n## Large Files\n")
		for _, file := range out.LargeFiles {
			fmt.Fprintf(&b, "- `%s` (%d LOC)\n", file.Path, file.LOC)
		}
	}
	if len(out.TopHotspots) > 0 {
		b.WriteString("\n## Top Hotspots\n\n| Risk | Name | File | LOC | Complexity | Fan-in |\n|---:|---|---|---:|---:|---:|\n")
		for _, hotspot := range out.TopHotspots {
			fmt.Fprintf(&b, "| %d | `%s` | `%s` | %d | %d | %d |\n",
				hotspot.RiskScore, hotspot.Name, hotspot.File, hotspot.LOC, hotspot.Complexity, hotspot.FanIn)
		}
	}
	if len(out.Summary) > 0 {
		fmt.Fprintf(&b, "\n## Cached Summary\n\n- Sync state: `%s`\n\n```json\n%s\n```\n", out.SyncState, out.Summary)
	}
	writePayloadFooter(&b, out.Truncated, out.TotalCount, out.Hint)
	return b.String()
}

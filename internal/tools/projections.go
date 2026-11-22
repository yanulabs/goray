package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

type generatedOmitted struct {
	Files   int `json:"files"`
	Symbols int `json:"symbols"`
	LOC     int `json:"loc"`
}

type compactModule struct {
	Name             string `json:"name"`
	Packages         int    `json:"packages"`
	Files            int    `json:"files"`
	GeneratedFiles   int    `json:"generated_files"`
	GeneratedSymbols int    `json:"generated_symbols"`
	GeneratedLOC     int    `json:"generated_loc"`
	UntrackedFiles   int    `json:"untracked_files"`
	ProdLOC          int    `json:"prod_loc"`
	TestLOC          int    `json:"test_loc"`
	TestOnly         bool   `json:"test_only,omitempty"`
	AllUntracked     bool   `json:"all_untracked,omitempty"`
	RootExported     int    `json:"root_exported"`
	PortsCalls       int    `json:"ports_calls"`
	Goroutines       int    `json:"goroutines"`
	FanIn            int    `json:"fan_in"`
	FanOut           int    `json:"fan_out"`
}

type compactEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Symbols int    `json:"symbols"`
}

type compactCodebase struct {
	ModulePath       string             `json:"module_path"`
	Packages         int                `json:"packages"`
	ModuleCount      int                `json:"module_count"`
	Symbols          int                `json:"symbols"`
	GeneratedFiles   int                `json:"generated_files"`
	GeneratedSymbols int                `json:"generated_symbols"`
	GeneratedLOC     int                `json:"generated_loc"`
	UntrackedFiles   int                `json:"untracked_files"`
	Detail           string             `json:"detail"`
	SummaryOnly      bool               `json:"summary_only"`
	SortBy           string             `json:"sort_by"`
	Modules          []compactModule    `json:"modules"`
	TotalModules     int                `json:"total_modules"`
	OmittedModules   int                `json:"omitted_modules"`
	OmittedCount     int                `json:"omitted_count"`
	CrossModuleEdges []compactEdge      `json:"cross_module_edges,omitempty"`
	ModuleCycles     []string           `json:"module_cycles,omitempty"`
	LargeFiles       []index.FileMetric `json:"large_files,omitempty"`
	TotalLargeFiles  int                `json:"total_large_files,omitempty"`
	TopHotspots      []index.Hotspot    `json:"top_hotspots,omitempty"`
	TotalHotspots    int                `json:"total_hotspots,omitempty"`
	Truncated        bool               `json:"truncated"`
	TotalCount       int                `json:"total_count"`
	ReturnedCount    int                `json:"returned_count"`
	Hint             string             `json:"hint,omitempty"`
	IndexState       index.State        `json:"index_state"`
}

type compactPackage struct {
	PkgPath          string `json:"pkg_path"`
	RelPath          string `json:"rel_path"`
	Name             string `json:"name"`
	ProdFiles        int    `json:"prod_files"`
	Files            int    `json:"files"`
	TestFiles        int    `json:"test_files"`
	ProdLOC          int    `json:"prod_loc"`
	TestLOC          int    `json:"test_loc"`
	Exported         int    `json:"exported"`
	PortsCalls       int    `json:"ports_calls"`
	Goroutines       int    `json:"goroutines"`
	GeneratedFiles   int    `json:"generated_files"`
	GeneratedSymbols int    `json:"generated_symbols"`
	GeneratedLOC     int    `json:"generated_loc"`
	UntrackedFiles   int    `json:"untracked_files"`
	TestOnly         bool   `json:"test_only,omitempty"`
}

type moduleEdgeCount struct {
	Module  string `json:"module"`
	Symbols int    `json:"symbols"`
}

type dependencyEdges struct {
	Out []moduleEdgeCount `json:"out"`
	In  []moduleEdgeCount `json:"in"`
}

type compactModuleOverview struct {
	Module           compactModule      `json:"module"`
	PackageRollups   []compactPackage   `json:"package_rollups"`
	Subpackages      []compactPackage   `json:"subpackages"`
	TotalPackages    int                `json:"total_packages"`
	DependencyEdges  dependencyEdges    `json:"dependency_edges"`
	TopHotspots      []index.Hotspot    `json:"top_hotspots"`
	TotalHotspots    int                `json:"total_hotspots"`
	LargeFiles       []index.FileMetric `json:"large_files,omitempty"`
	TotalLargeFiles  int                `json:"total_large_files"`
	GeneratedOmitted *generatedOmitted  `json:"generated_omitted,omitempty"`
	IncludeGenerated bool               `json:"include_generated"`
	Truncated        bool               `json:"truncated"`
	TotalCount       int                `json:"total_count"`
	ReturnedCount    int                `json:"returned_count"`
	Hint             string             `json:"hint,omitempty"`
	IndexState       index.State        `json:"index_state"`
}

func compactModuleSummary(idx *index.Index, mod *index.ModSummary) compactModule {
	return compactModule{
		Name: mod.Name, Packages: mod.Packages, Files: mod.Files,
		GeneratedFiles: mod.GeneratedFiles, GeneratedSymbols: mod.GeneratedSymbols,
		GeneratedLOC: mod.GeneratedLOC, UntrackedFiles: mod.UntrackedFiles,
		ProdLOC: mod.ProdLOC, TestLOC: mod.TestLOC, TestOnly: mod.TestOnly,
		AllUntracked: mod.AllUntracked, RootExported: mod.RootExported,
		PortsCalls: mod.PortsCalls, Goroutines: mod.Goroutines,
		FanIn: len(mod.InModules), FanOut: len(mod.OutModules),
	}
}

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
		out = append(out, compactModuleSummary(idx, mod))
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

func sortedModuleKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func generatedTotals(idx *index.Index) (files, symbols, loc int) {
	for _, pkg := range idx.Packages {
		files += pkg.GeneratedFiles
		symbols += pkg.GeneratedSymbols
		loc += pkg.GeneratedLOC
	}
	return files, symbols, loc
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
	shown := allModules
	omittedModules := 0
	if topModules > 0 && len(shown) > topModules {
		omittedModules = len(shown) - topModules
		shown = shown[:topModules]
	}
	generatedFiles, generatedSymbols, generatedLOC := generatedTotals(idx)
	out := compactCodebase{
		ModulePath: idx.ModulePath, Packages: len(idx.Packages), ModuleCount: len(idx.Modules),
		Symbols: len(idx.Symbols), GeneratedFiles: generatedFiles, GeneratedSymbols: generatedSymbols,
		GeneratedLOC: generatedLOC, Detail: detail, SummaryOnly: summaryOnly, SortBy: sortBy,
		Modules: shown, TotalModules: totalModules, OmittedModules: omittedModules, OmittedCount: omittedModules,
		TotalCount: totalModules, ReturnedCount: len(shown), IndexState: t.src.Status().IndexState,
	}
	if !summaryOnly {
		out.CrossModuleEdges = compactEdges(idx)
		cycles := idx.ModuleCycles()
		out.ModuleCycles = make([]string, 0, len(cycles))
		for _, cycle := range cycles {
			out.ModuleCycles = append(out.ModuleCycles, strings.Join(cycle, " -> "))
		}
		large := make([]index.FileMetric, 0, len(idx.LargeFiles))
		for _, file := range idx.LargeFiles {
			if !file.Generated {
				large = append(large, file)
			}
		}
		out.TotalLargeFiles = len(large)
		out.LargeFiles = limitFiles(large, 10)
		hotspots, total := idx.Hotspots(index.HotspotOptions{Target: "func", SortBy: "risk", Limit: 10})
		out.TotalHotspots = total
		out.TopHotspots = hotspots
	}

	out.Truncated = omittedModules > 0 || summaryOnly
	if !summaryOnly && (len(out.LargeFiles) < out.TotalLargeFiles || len(out.TopHotspots) < out.TotalHotspots) {
		out.Truncated = true
	}
	out.Hint = codebaseHint(summaryOnly, topModules, omittedModules, len(out.LargeFiles), out.TotalLargeFiles, len(out.TopHotspots), out.TotalHotspots)

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

func (t *toolset) compactModuleOverview(idx *index.Index, mod *index.ModSummary, args map[string]any) (string, bool) {
	includeGenerated := boolArg(args, "include_generated", false)
	packages := compactPackageRollups(idx, mod.Name)
	hotspots, totalHotspots := idx.Hotspots(index.HotspotOptions{
		Target: "func", SortBy: "risk", Limit: 10, Module: mod.Name,
		IncludeGenerated: includeGenerated,
	})
	large := make([]index.FileMetric, 0, len(mod.LargeFiles))
	for _, file := range mod.LargeFiles {
		if includeGenerated || !file.Generated {
			large = append(large, file)
		}
	}
	totalLarge := len(large)
	out := compactModuleOverview{
		Module: compactModuleSummary(idx, mod), PackageRollups: packages, Subpackages: packages, TotalPackages: len(packages),
		DependencyEdges: compactModuleEdges(mod), TopHotspots: hotspots, TotalHotspots: totalHotspots,
		LargeFiles: limitFiles(large, 10), TotalLargeFiles: totalLarge, IncludeGenerated: includeGenerated,
		TotalCount: len(packages), ReturnedCount: len(packages), IndexState: t.src.Status().IndexState,
	}
	if !includeGenerated && (mod.GeneratedFiles > 0 || mod.GeneratedSymbols > 0 || mod.GeneratedLOC > 0) {
		out.GeneratedOmitted = &generatedOmitted{Files: mod.GeneratedFiles, Symbols: mod.GeneratedSymbols, LOC: mod.GeneratedLOC}
	}
	out.Truncated = out.GeneratedOmitted != nil || len(out.LargeFiles) < totalLarge || len(out.TopHotspots) < totalHotspots
	hints := make([]string, 0, 3)
	if out.GeneratedOmitted != nil {
		hints = append(hints, fmt.Sprintf(`set include_generated=true to include %d generated symbols and %d generated LOC`, mod.GeneratedSymbols, mod.GeneratedLOC))
	}
	if len(out.LargeFiles) < totalLarge {
		hints = append(hints, fmt.Sprintf("large files are capped at 10 of %d", totalLarge))
	}
	if len(out.TopHotspots) < totalHotspots {
		hints = append(hints, fmt.Sprintf("call get_hotspots with module=%q for all %d hotspots", mod.Name, totalHotspots))
	}
	out.Hint = strings.Join(hints, "; ")

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
	writePayloadFooter(&b, out.Truncated, out.TotalCount, out.Hint)
	return b.String()
}

func writePayloadFooter(b *strings.Builder, truncated bool, total int, hint string) {
	fmt.Fprintf(b, "\n## Payload\n\n- truncated: %t\n- total_count: %d\n", truncated, total)
	if hint != "" {
		fmt.Fprintf(b, "- hint: %s\n", hint)
	}
}

func limitFiles(items []index.FileMetric, limit int) []index.FileMetric {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

package render

import (
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

type markdown struct {
	baseRenderer
	mermaid mermaid
}

func newMarkdown() markdown {
	return markdown{baseRenderer: baseRenderer{name: "markdown"}, mermaid: newMermaid()}
}

func (m markdown) RenderCodebaseMap(idx *index.Index) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture Map: `%s`\n\n", idx.ModulePath)
	fmt.Fprintf(&b, "- **Packages:** %d | **Modules:** %d | **Symbols:** %d | **Generated files:** %d | **Untracked files:** %d\n\n",
		len(idx.Packages), len(idx.Modules), len(idx.Symbols), generatedFileCount(idx), untrackedFileCount(idx))

	b.WriteString("## Module Scorecard\n\n")
	b.WriteString("| Module | Pkgs | Files | Generated | Untracked | LOC | Fan-in | Fan-out |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mod := range idx.ModulesByLOC() {
		label := mod.Name
		if mod.TestOnly {
			label += " [test-only]"
		}
		if mod.AllUntracked {
			label += " [untracked]"
		}
		loc := fmt.Sprintf("%d", mod.ProdLOC)
		if mod.TestOnly {
			loc = fmt.Sprintf("%d test loc", mod.TestLOC)
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %s | %d | %d |\n",
			label, mod.Packages, mod.Files, mod.GeneratedFiles, mod.UntrackedFiles,
			loc, len(mod.InModules), len(mod.OutModules))
	}

	b.WriteString("\n## Cross-Module Graph\n\n")
	b.WriteString(m.mermaid.moduleGraph(idx))

	b.WriteString("\n## Cross-Module Dependencies\n\n")
	edges := moduleEdges(idx)
	if len(edges) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, edge := range edges {
			fmt.Fprintf(&b, "- `%s` -> `%s`: %s\n", edge.From, edge.To, strings.Join(edge.Symbols, ", "))
		}
	}

	b.WriteString("\n## Cross-Module Cycles\n\n")
	cycles := idx.ModuleCycles()
	if len(cycles) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, cycle := range cycles {
			fmt.Fprintf(&b, "- `%s`\n", strings.Join(cycle, " -> "))
		}
	}

	large := make([]index.FileMetric, 0, len(idx.LargeFiles))
	for _, file := range idx.LargeFiles {
		if !file.Generated {
			large = append(large, file)
		}
	}
	if len(large) > 0 {
		b.WriteString("\n## Large Files (non-generated, max 10)\n\n| LOC | File |\n|---:|---|\n")
		for _, file := range limit(large, 10) {
			label := file.Path
			if file.Untracked {
				label += " [untracked]"
			}
			fmt.Fprintf(&b, "| %d | `%s` |\n", file.LOC, label)
		}
	}

	b.WriteString("\n## Top Hotspots (non-generated, max 10)\n\n")
	hotspots := codebaseHotspots(idx, 10)
	if len(hotspots) == 0 {
		b.WriteString("- None\n")
	} else {
		b.WriteString("| Risk | Name | Module | File | LOC | Complexity | Fan-in |\n|---:|---|---|---|---:|---:|---:|\n")
		for _, h := range hotspots {
			fmt.Fprintf(&b, "| %d | `%s` | `%s` | `%s` | %d | %d | %d |\n",
				h.RiskScore, h.Name, h.Module, h.displayFile(), h.LOC, h.Complexity, h.FanIn)
		}
	}
	b.WriteString("\nUse `get_hotspots` and `get_module_overview` for deeper drill-down.\n")
	return b.String()
}

func (markdown) RenderModule(idx *index.Index, mod *index.ModSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Module `%s`\n\n", mod.Name)
	if mod.TestOnly {
		fmt.Fprintf(&b, "- Classification: **test-only** (no production files; %d test LOC)\n", mod.TestLOC)
	}
	fmt.Fprintf(&b, "- Prod LOC: %d | Test LOC: %d | Packages: %d\n", mod.ProdLOC, mod.TestLOC, mod.Packages)
	fmt.Fprintf(&b, "- Root Exported: %d | `.Ports()` calls: %d | Goroutines: %d\n\n",
		mod.RootExported, mod.PortsCalls, mod.Goroutines)

	b.WriteString("## Subpackages\n| RelPath | Prod LOC | Test LOC | Exported | `.Ports()` |\n|---|---:|---:|---:|---:|\n")
	for _, pkg := range modulePackages(idx, mod.Name) {
		label := pkg.RelPath
		if pkg.TestOnly {
			label += " [test-only]"
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d |\n", label, pkg.ProdLOC, pkg.TestLOC, pkg.Exported, pkg.PortsCalls)
	}

	b.WriteString("\n## Outgoing Module Deps\n")
	if len(mod.OutModules) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, dst := range sortedMapKeys(mod.OutModules) {
			fmt.Fprintf(&b, "- **`%s`**: %s\n", dst, strings.Join(mod.OutModules[dst], ", "))
		}
	}

	b.WriteString("\n## Incoming Module Callers\n")
	if len(mod.InModules) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, src := range sortedMapKeys(mod.InModules) {
			fmt.Fprintf(&b, "- **`%s`**: %s\n", src, strings.Join(mod.InModules[src], ", "))
		}
	}

	if len(mod.LargeFiles) > 0 {
		b.WriteString("\n## Large Files\n")
		for _, file := range mod.LargeFiles {
			fmt.Fprintf(&b, "- `%s` (%d LOC)\n", file.Path, file.LOC)
		}
	}
	return b.String()
}

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
	fmt.Fprintf(&b, "- **Packages:** %d | **Modules:** %d | **Symbols:** %d\n\n",
		len(idx.Packages), len(idx.Modules), len(idx.Symbols))

	b.WriteString("## Module Scorecard\n\n")
	b.WriteString("| Module | Pkgs | Prod LOC | Test LOC | Root Exp | Out | In | `.Ports()` | Go |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mod := range idx.ModulesByLOC() {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			mod.Name, mod.Packages, mod.ProdLOC, mod.TestLOC, mod.RootExported,
			len(mod.OutModules), len(mod.InModules), mod.PortsCalls, mod.Goroutines)
	}

	b.WriteString("\n## Cross-Module Graph\n\n")
	b.WriteString(m.mermaid.moduleGraph(idx))
	b.WriteString("\n## Cross-Module Symbol Coupling\n\n| Source | Target | Symbols |\n|---|---|---|\n")
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		for _, dst := range sortedMapKeys(mod.OutModules) {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` |\n", mod.Name, dst, strings.Join(mod.OutModules[dst], "`, `"))
		}
	}

	if len(idx.LargeFiles) > 0 {
		b.WriteString("\n## Bloated Production Files (>=400 LOC)\n\n| LOC | File |\n|---:|---|\n")
		for _, file := range limit(idx.LargeFiles, 25) {
			fmt.Fprintf(&b, "| %d | `%s` |\n", file.LOC, file.Path)
		}
	}
	return b.String()
}

func (markdown) RenderModule(idx *index.Index, mod *index.ModSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Module `%s`\n\n", mod.Name)
	fmt.Fprintf(&b, "- Prod LOC: %d | Test LOC: %d | Packages: %d\n", mod.ProdLOC, mod.TestLOC, mod.Packages)
	fmt.Fprintf(&b, "- Root Exported: %d | `.Ports()` calls: %d | Goroutines: %d\n\n",
		mod.RootExported, mod.PortsCalls, mod.Goroutines)

	b.WriteString("## Subpackages\n| RelPath | Prod LOC | Test LOC | Exported | `.Ports()` |\n|---|---:|---:|---:|---:|\n")
	for _, pkg := range modulePackages(idx, mod.Name) {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d |\n", pkg.RelPath, pkg.ProdLOC, pkg.TestLOC, pkg.Exported, pkg.PortsCalls)
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

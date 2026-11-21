// Package render formats index snapshots as Markdown, Mermaid and DOT.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

// CodebaseMapMarkdown renders the architecture scorecard.
func CodebaseMapMarkdown(idx *index.Index) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture Map: `%s`\n\n", idx.ModulePath)
	fmt.Fprintf(&b, "- **Packages:** %d | **Modules:** %d | **Symbols:** %d\n\n",
		len(idx.Packages), len(idx.Modules), len(idx.Symbols))

	b.WriteString("## Module Scorecard\n\n")
	b.WriteString("| Module | Pkgs | Prod LOC | Test LOC | Root Exp | Out | In | `.Ports()` | Go |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range idx.ModulesByLOC() {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			m.Name, m.Packages, m.ProdLOC, m.TestLOC, m.RootExported,
			len(m.OutModules), len(m.InModules), m.PortsCalls, m.Goroutines)
	}

	b.WriteString("\n## Cross-Module Graph\n\n")
	b.WriteString(ModuleMermaid(idx))
	b.WriteString("\n## Cross-Module Symbol Coupling\n\n| Source | Target | Symbols |\n|---|---|---|\n")
	for _, name := range idx.SortedModuleNames() {
		m := idx.Modules[name]
		for _, dst := range sortedMapKeys(m.OutModules) {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` |\n", m.Name, dst, strings.Join(m.OutModules[dst], "`, `"))
		}
	}

	if len(idx.LargeFiles) > 0 {
		b.WriteString("\n## Bloated Production Files (>=400 LOC)\n\n| LOC | File |\n|---:|---|\n")
		for _, f := range limit(idx.LargeFiles, 25) {
			fmt.Fprintf(&b, "| %d | `%s` |\n", f.LOC, f.Path)
		}
	}
	return b.String()
}

// ModuleMermaid renders the cross-module dependency graph.
func ModuleMermaid(idx *index.Index) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	for _, name := range idx.SortedModuleNames() {
		m := idx.Modules[name]
		fmt.Fprintf(&b, "  %s[\"%s<br/>%.1fk LOC | %d exp\"]\n",
			safeID(m.Name), m.Name, float64(m.ProdLOC)/1000, m.RootExported)
	}
	for _, name := range idx.SortedModuleNames() {
		m := idx.Modules[name]
		for _, dst := range sortedMapKeys(m.OutModules) {
			fmt.Fprintf(&b, "  %s -->|%d sym| %s\n", safeID(m.Name), len(m.OutModules[dst]), safeID(dst))
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// CodebaseMapDOT renders modules as a Graphviz digraph.
func CodebaseMapDOT(idx *index.Index) string {
	var b strings.Builder
	b.WriteString("digraph GoModules {\n  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fontname=\"Helvetica\", fontsize=10];\n")
	b.WriteString("  edge [fontname=\"Helvetica\", fontsize=9];\n\n")
	for _, name := range idx.SortedModuleNames() {
		m := idx.Modules[name]
		fill := "#e8f5e9"
		switch {
		case m.ProdLOC > 10000 || m.RootExported > 150:
			fill = "#ffcdd2"
		case m.ProdLOC > 5000 || m.RootExported > 80:
			fill = "#fff9c4"
		}
		fmt.Fprintf(&b, "  %s [label=\"%s\\n%.1fk prod | %d exp | %d ports\", fillcolor=\"%s\"];\n",
			safeID(m.Name), m.Name, float64(m.ProdLOC)/1000, m.RootExported, m.PortsCalls, fill)
	}
	b.WriteString("\n")
	for _, name := range idx.SortedModuleNames() {
		m := idx.Modules[name]
		for _, dst := range sortedMapKeys(m.OutModules) {
			count := len(m.OutModules[dst])
			color := "#546e7a"
			if count >= 5 {
				color = "#c62828"
			}
			fmt.Fprintf(&b, "  %s -> %s [label=\"%d\", color=\"%s\"];\n",
				safeID(m.Name), safeID(dst), count, color)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// ModuleOverview renders one module in detail.
func ModuleOverview(idx *index.Index, ms *index.ModSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Module `%s`\n\n", ms.Name)
	fmt.Fprintf(&b, "- Prod LOC: %d | Test LOC: %d | Packages: %d\n", ms.ProdLOC, ms.TestLOC, ms.Packages)
	fmt.Fprintf(&b, "- Root Exported: %d | `.Ports()` calls: %d | Goroutines: %d\n\n",
		ms.RootExported, ms.PortsCalls, ms.Goroutines)

	b.WriteString("## Subpackages\n| RelPath | Prod LOC | Test LOC | Exported | `.Ports()` |\n|---|---:|---:|---:|---:|\n")
	var pkgs []*index.PkgInfo
	for _, p := range idx.Packages {
		if p.ModuleArea == ms.Name {
			pkgs = append(pkgs, p)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].RelPath < pkgs[j].RelPath })
	for _, p := range pkgs {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d |\n", p.RelPath, p.ProdLOC, p.TestLOC, p.Exported, p.PortsCalls)
	}

	b.WriteString("\n## Outgoing Module Deps\n")
	if len(ms.OutModules) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, dst := range sortedMapKeys(ms.OutModules) {
			fmt.Fprintf(&b, "- **`%s`**: %s\n", dst, strings.Join(ms.OutModules[dst], ", "))
		}
	}

	b.WriteString("\n## Incoming Module Callers\n")
	if len(ms.InModules) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, src := range sortedMapKeys(ms.InModules) {
			fmt.Fprintf(&b, "- **`%s`**: %s\n", src, strings.Join(ms.InModules[src], ", "))
		}
	}

	if len(ms.LargeFiles) > 0 {
		b.WriteString("\n## Large Files\n")
		for _, f := range ms.LargeFiles {
			fmt.Fprintf(&b, "- `%s` (%d LOC)\n", f.Path, f.LOC)
		}
	}
	return b.String()
}

func safeID(s string) string {
	return strings.NewReplacer("-", "_", "/", "_", ".", "_").Replace(s)
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func limit[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

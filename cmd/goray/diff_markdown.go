package main

import (
	"fmt"
	"io"
	"strings"
)

func printDiffMarkdown(w io.Writer, report diffReport) error {
	var b strings.Builder
	writeDiffSummaryMarkdown(&b, report)
	writeDiffCyclesMarkdown(&b, report.Cycles)
	writeDiffAPIMarkdown(&b, report.API)
	writeDiffHotspotsMarkdown(&b, report.Hotspots)
	writeDiffDeadCodeMarkdown(&b, report.Dead)
	if len(report.Failed) > 0 {
		fmt.Fprintf(&b, "\n**Failed gates:** %s\n", strings.Join(report.Failed, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeDiffSummaryMarkdown(b *strings.Builder, report diffReport) {
	fmt.Fprintf(b, "# goray diff: `%s` vs working tree\n\n", report.BaseRef)
	fmt.Fprintf(b, "| Metric | Base | Head | Delta |\n|---|---:|---:|---:|\n")
	row := func(name string, before, after int) {
		fmt.Fprintf(b, "| %s | %d | %d | %+d |\n", name, before, after, after-before)
	}
	row("Packages", report.Summary.PackagesBase, report.Summary.Packages)
	row("Files", report.Summary.FilesBase, report.Summary.Files)
	row("LOC", report.Summary.LOCBase, report.Summary.LOC)
	row("Symbols", report.Summary.SymbolsBase, report.Summary.Symbols)
	row("Module edges", report.Summary.ModuleEdgesBase, report.Summary.ModuleEdges)
	fmt.Fprintf(b, "| Untested hotspots | - | %d | - |\n", report.Summary.UntestedHotspots)
}

func writeDiffCyclesMarkdown(b *strings.Builder, cycles diffCycles) {
	fmt.Fprintf(b, "\n## Cycles\n\n- New: %d\n- Resolved: %d\n", len(cycles.New), len(cycles.Resolved))
	for _, cycle := range cycles.New {
		fmt.Fprintf(b, "  - `%s`\n", cycle)
	}
	for _, cycle := range cycles.Resolved {
		fmt.Fprintf(b, "  - resolved `%s`\n", cycle)
	}
}

func writeDiffAPIMarkdown(b *strings.Builder, api diffAPI) {
	fmt.Fprintf(b, "\n## Breaking API\n\n- Removed: %d\n- Modified: %d\n- Added: %d\n",
		len(api.Removed), len(api.Modified), len(api.Added))
	for _, change := range api.Removed {
		fmt.Fprintf(b, "  - removed `%s` (%s:%d)\n", change.Name, change.File, change.Line)
	}
	for _, change := range api.Modified {
		fmt.Fprintf(b, "  - modified `%s` (%s:%d): `%s` -> `%s`\n", change.Name, change.File, change.Line, change.Before, change.After)
	}
}

func writeDiffHotspotsMarkdown(b *strings.Builder, hotspots diffHotspots) {
	fmt.Fprintf(b, "\n## Hotspots\n\n- New untested complex functions: %d\n", len(hotspots.New))
	for _, hotspot := range hotspots.New {
		fmt.Fprintf(b, "  - `%s` CC=%d LOC=%d (%s:%d)\n", hotspot.Name, hotspot.AfterCC, hotspot.AfterLOC, hotspot.File, hotspot.Line)
	}
	if len(hotspots.TopDelta) > 0 {
		b.WriteString("\n| Function | CC | LOC | ΔCC | ΔLOC |\n|---|---:|---:|---:|---:|\n")
		for _, hotspot := range hotspots.TopDelta {
			fmt.Fprintf(b, "| `%s` | %d -> %d | %d -> %d | %+d | %+d |\n",
				hotspot.Name, hotspot.BeforeCC, hotspot.AfterCC, hotspot.BeforeLOC, hotspot.AfterLOC, hotspot.DeltaCC, hotspot.DeltaLOC)
		}
	}
}

func writeDiffDeadCodeMarkdown(b *strings.Builder, dead diffDeadCode) {
	fmt.Fprintf(b, "\n## Dead Code\n\n- New: %d\n- Removed: %d\n", len(dead.New), len(dead.Removed))
	for _, item := range dead.New {
		fmt.Fprintf(b, "  - new `%s` (%s:%d)\n", item.Name, item.File, item.Line)
	}
	for _, item := range dead.Removed {
		fmt.Fprintf(b, "  - removed `%s` (%s:%d)\n", item.Name, item.File, item.Line)
	}
}

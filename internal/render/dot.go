package render

import (
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

type dot struct{ baseRenderer }

func newDOT() dot {
	return dot{baseRenderer{name: "dot"}}
}

func (dot) RenderCodebaseMap(idx *index.Index) string {
	var b strings.Builder
	b.WriteString("digraph GoModules {\n  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fontname=\"Helvetica\", fontsize=10];\n")
	b.WriteString("  edge [fontname=\"Helvetica\", fontsize=9];\n\n")
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		fill := "#e8f5e9"
		switch {
		case mod.ProdLOC > 10000 || mod.RootExported > 150:
			fill = "#ffcdd2"
		case mod.ProdLOC > 5000 || mod.RootExported > 80:
			fill = "#fff9c4"
		}
		fmt.Fprintf(&b, "  %s [label=\"%s\\n%.1fk prod | %d exp | %d ports\", fillcolor=\"%s\"];\n",
			safeID(mod.Name), mod.Name, float64(mod.ProdLOC)/1000, mod.RootExported, mod.PortsCalls, fill)
	}
	b.WriteString("\n")
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		for _, dst := range sortedMapKeys(mod.OutModules) {
			count := len(mod.OutModules[dst])
			color := "#546e7a"
			if count >= 5 {
				color = "#c62828"
			}
			fmt.Fprintf(&b, "  %s -> %s [label=\"%d\", color=\"%s\"];\n",
				safeID(mod.Name), safeID(dst), count, color)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func (dot) RenderGraph(graph index.Graph) string {
	var b strings.Builder
	b.WriteString("digraph Subgraph {\n  rankdir=LR;\n")
	for _, edge := range graph.Edges {
		fmt.Fprintf(&b, "  \"%s\" -> \"%s\";\n", edge.From, edge.To)
	}
	b.WriteString("}\n")
	return b.String()
}

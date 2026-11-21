package render

import (
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

type mermaid struct{ baseRenderer }

func newMermaid() mermaid {
	return mermaid{baseRenderer{name: "mermaid"}}
}

func (m mermaid) RenderCodebaseMap(idx *index.Index) string {
	return m.moduleGraph(idx)
}

func (m mermaid) RenderGraph(graph index.Graph) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	for _, edge := range graph.Edges {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n",
			safeID(edge.From), edge.From, safeID(edge.To), edge.To)
	}
	b.WriteString("```\n")
	return b.String()
}

func (m mermaid) moduleGraph(idx *index.Index) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		fmt.Fprintf(&b, "  %s[\"%s<br/>%.1fk LOC | %d exp\"]\n",
			safeID(mod.Name), mod.Name, float64(mod.ProdLOC)/1000, mod.RootExported)
	}
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		for _, dst := range sortedMapKeys(mod.OutModules) {
			fmt.Fprintf(&b, "  %s -->|%d sym| %s\n", safeID(mod.Name), len(mod.OutModules[dst]), safeID(dst))
		}
	}
	b.WriteString("```\n")
	return b.String()
}

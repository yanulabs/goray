package render

import (
	"fmt"
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

// DependencyMermaid renders a package subgraph.
func DependencyMermaid(g index.Graph) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %s[\"%s\"] --> %s[\"%s\"]\n", safeID(e.From), e.From, safeID(e.To), e.To)
	}
	b.WriteString("```\n")
	return b.String()
}

// DependencyDOT renders a package subgraph.
func DependencyDOT(g index.Graph) string {
	var b strings.Builder
	b.WriteString("digraph Subgraph {\n  rankdir=LR;\n")
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  \"%s\" -> \"%s\";\n", e.From, e.To)
	}
	b.WriteString("}\n")
	return b.String()
}

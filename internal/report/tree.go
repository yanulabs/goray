package report

import (
	"sort"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func buildTree(idx *index.Index, cached map[string]cache.Cached, canonical map[string]*index.Symbol) []*render.HTMLTreeNode {
	byID := make(map[string]*render.HTMLTreeNode, len(idx.Summary.Symbols))
	for _, summary := range idx.Summary.Symbols {
		kind := summary.Kind
		if kind == "type" {
			if sym := canonical[summary.ID]; sym != nil && (sym.Kind == "interface" || sym.Kind == "struct") {
				kind = sym.Kind
			}
		}
		switch kind {
		case "package", "module", "file", "type", "struct", "interface", "func", "method":
			byID[summary.ID] = &render.HTMLTreeNode{
				ID: summary.ID, Kind: kind,
				Label:     symbolLabel(summary, canonical[summary.ID]),
				SyncState: stateFor(cached, summary),
			}
		}
	}
	roots := make([]*render.HTMLTreeNode, 0)
	for _, summary := range idx.Summary.Symbols {
		node := byID[summary.ID]
		if node == nil {
			continue
		}
		if parent := byID[summary.ParentID]; parent != nil {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	var sortNodes func([]*render.HTMLTreeNode)
	sortNodes = func(nodes []*render.HTMLTreeNode) {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Kind != nodes[j].Kind {
				return kindRank(nodes[i].Kind) < kindRank(nodes[j].Kind)
			}
			return nodes[i].Label < nodes[j].Label
		})
		for _, node := range nodes {
			sortNodes(node.Children)
		}
	}
	sortNodes(roots)
	return roots
}

func kindRank(kind string) int {
	switch kind {
	case "module":
		return 0
	case "package":
		return 1
	case "file":
		return 2
	case "type", "struct", "interface":
		return 3
	case "func", "method":
		return 4
	}
	return 5
}

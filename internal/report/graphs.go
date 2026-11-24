package report

import (
	"sort"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func buildModuleGraph(idx *index.Index, cached map[string]cache.Cached) render.HTMLGraph {
	graph := render.HTMLGraph{Nodes: make([]render.HTMLGraphNode, 0, len(idx.Modules)), Edges: make([]render.HTMLGraphEdge, 0)}
	for _, name := range idx.SortedModuleNames() {
		module := idx.Modules[name]
		id := "mod:" + name
		summary := idx.Summary.Get(id)
		node := render.HTMLGraphNode{
			ID: id, Label: name, Kind: "module", Module: name, LOC: module.ProdLOC,
			TestLOC: module.TestLOC, TestOnly: module.TestOnly,
			FanIn: len(module.InModules), FanOut: len(module.OutModules),
			Eligible:  summary != nil && summary.Eligible,
			Untracked: module.UntrackedFiles > 0,
			SyncState: stateForID(cached, id, summary), Summary: summaryView(cached[id]),
		}
		graph.Nodes = append(graph.Nodes, node)
		for _, target := range sortedKeys(module.OutModules) {
			graph.Edges = append(graph.Edges, render.HTMLGraphEdge{From: id, To: "mod:" + target, Weight: len(module.OutModules[target])})
		}
	}
	return graph
}

func buildPackageGraph(idx *index.Index, cached map[string]cache.Cached) render.HTMLGraph {
	graph := render.HTMLGraph{Nodes: make([]render.HTMLGraphNode, 0, len(idx.Packages)), Edges: make([]render.HTMLGraphEdge, 0)}
	paths := make([]string, 0, len(idx.Packages))
	for pkgPath := range idx.Packages {
		paths = append(paths, pkgPath)
	}
	sort.Strings(paths)
	for _, pkgPath := range paths {
		pkg := idx.Packages[pkgPath]
		id := "pkg:" + pkgPath
		summary := idx.Summary.Get(id)
		node := render.HTMLGraphNode{
			ID: id, Label: pkg.RelPath, Kind: "package", Module: pkg.ModuleArea, LOC: pkg.ProdLOC,
			TestLOC: pkg.TestLOC, TestOnly: pkg.TestOnly,
			FanIn: len(idx.RevImports[pkgPath]), FanOut: len(pkg.Imports),
			Eligible:  summary != nil && summary.Eligible,
			Untracked: pkg.UntrackedFiles > 0,
			SyncState: stateForID(cached, id, summary), Summary: summaryView(cached[id]),
		}
		graph.Nodes = append(graph.Nodes, node)
		for _, imported := range pkg.Imports {
			graph.Edges = append(graph.Edges, render.HTMLGraphEdge{From: id, To: "pkg:" + imported, Weight: 1})
		}
	}
	return graph
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

package index

import (
	"sort"
)

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Graph struct {
	Root  string `json:"root"`
	Nodes int    `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Fan struct {
	Pkg    string `json:"pkg"`
	FanOut int    `json:"fan_out"`
	FanIn  int    `json:"fan_in"`
}

// CallGraph walks callers/callees breadth-first from rootID.
func (idx *Index) CallGraph(rootID, direction string, depth, limit int) Graph {
	seen := map[string]int{rootID: 0}
	queue := []string{rootID}
	edges := make([]Edge, 0)

	for len(queue) > 0 && len(seen) < limit {
		cur := queue[0]
		queue = queue[1:]
		d := seen[cur]
		if d >= depth {
			continue
		}

		if direction == "callees" || direction == "both" {
			for _, next := range idx.Callees[cur] {
				edges = append(edges, Edge{From: cur, To: next})
				if _, exists := seen[next]; !exists && len(seen) < limit {
					seen[next] = d + 1
					queue = append(queue, next)
				}
			}
		}
		if direction == "callers" || direction == "both" {
			for _, prev := range idx.Callers[cur] {
				edges = append(edges, Edge{From: prev, To: cur})
				if _, exists := seen[prev]; !exists && len(seen) < limit {
					seen[prev] = d + 1
					queue = append(queue, prev)
				}
			}
		}
	}
	return Graph{Root: rootID, Nodes: len(seen), Edges: edges}
}

// DependencyGraph walks package imports or reverse imports from rootPkg.
func (idx *Index) DependencyGraph(rootPkg, direction string, depth int) Graph {
	seen := map[string]int{rootPkg: 0}
	queue := []string{rootPkg}
	edges := make([]Edge, 0)
	edge := func(from, to string) Edge {
		return Edge{From: idx.RelPath(from), To: idx.RelPath(to)}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		d := seen[cur]
		if d >= depth {
			continue
		}

		if direction == "out" || direction == "both" {
			if info, ok := idx.Packages[cur]; ok {
				for _, next := range info.Imports {
					edges = append(edges, edge(cur, next))
					if _, exists := seen[next]; !exists {
						seen[next] = d + 1
						queue = append(queue, next)
					}
				}
			}
		}
		if direction == "in" || direction == "both" {
			for _, prev := range idx.RevImports[cur] {
				edges = append(edges, edge(prev, cur))
				if _, exists := seen[prev]; !exists {
					seen[prev] = d + 1
					queue = append(queue, prev)
				}
			}
		}
	}
	return Graph{Root: idx.RelPath(rootPkg), Nodes: len(seen), Edges: edges}
}

// FanStats ranks packages by fan-out, then fan-in.
func (idx *Index) FanStats(limit int) []Fan {
	stats := make([]Fan, 0, len(idx.Packages))
	for pkg, info := range idx.Packages {
		stats = append(stats, Fan{Pkg: info.RelPath, FanOut: len(info.Imports), FanIn: len(idx.RevImports[pkg])})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].FanOut != stats[j].FanOut {
			return stats[i].FanOut > stats[j].FanOut
		}
		if stats[i].FanIn != stats[j].FanIn {
			return stats[i].FanIn > stats[j].FanIn
		}
		return stats[i].Pkg < stats[j].Pkg
	})
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}
	return stats
}

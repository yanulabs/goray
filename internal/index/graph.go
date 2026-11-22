package index

import (
	"sort"
)

// Edge is one directed graph edge.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"edge_kind,omitempty"`
	Via  string `json:"via_interface,omitempty"`
}

// Graph is a bounded call or dependency graph projection.
type Graph struct {
	Root  string `json:"root"`
	Nodes int    `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Fan is one package's fan-in/fan-out pair.
type Fan struct {
	Pkg    string `json:"pkg"`
	FanOut int    `json:"fan_out"`
	FanIn  int    `json:"fan_in"`
}

type graphStep struct {
	neighbors func(string) []string
	edge      func(cur, next string) Edge
}

// CallGraph walks callers/callees breadth-first from rootID.
func (idx *Index) CallGraph(rootID, direction string, depth, limit int) Graph {
	return idx.CallGraphWithOptions(rootID, direction, depth, limit, true)
}

// CallGraphWithOptions optionally includes interface_dispatch edges.
func (idx *Index) CallGraphWithOptions(rootID, direction string, depth, limit int, includeInterfaceDispatch bool) Graph {
	steps := make([]graphStep, 0, 2)
	if direction == "callees" || direction == "both" {
		steps = append(steps, graphStep{
			neighbors: func(id string) []string {
				return idx.filterCallees(id, includeInterfaceDispatch)
			},
			edge: func(cur, next string) Edge { return idx.callEdge(cur, next) },
		})
	}
	if direction == "callers" || direction == "both" {
		steps = append(steps, graphStep{
			neighbors: func(id string) []string {
				return idx.filterCallers(id, includeInterfaceDispatch)
			},
			edge: func(cur, next string) Edge { return idx.callEdge(next, cur) },
		})
	}
	return walkGraph(rootID, depth, limit, steps)
}

func (idx *Index) filterCallees(id string, includeDispatch bool) []string {
	if includeDispatch {
		return idx.Callees[id]
	}
	out := make([]string, 0, len(idx.Callees[id]))
	for _, callee := range idx.Callees[id] {
		if edge, ok := idx.CallEdgeKinds[id+"\x00"+callee]; ok && edge.Kind == "interface_dispatch" {
			continue
		}
		out = append(out, callee)
	}
	return out
}

func (idx *Index) filterCallers(id string, includeDispatch bool) []string {
	if includeDispatch {
		return idx.Callers[id]
	}
	out := make([]string, 0, len(idx.Callers[id]))
	for _, caller := range idx.Callers[id] {
		if edge, ok := idx.CallEdgeKinds[caller+"\x00"+id]; ok && edge.Kind == "interface_dispatch" {
			continue
		}
		out = append(out, caller)
	}
	return out
}

func (idx *Index) callEdge(from, to string) Edge {
	edge := Edge{From: from, To: to}
	if kind, ok := idx.CallEdgeKinds[from+"\x00"+to]; ok {
		edge.Kind = kind.Kind
		edge.Via = kind.Via
	}
	return edge
}

// DependencyGraph walks package imports or reverse imports from rootPkg.
func (idx *Index) DependencyGraph(rootPkg, direction string, depth int) Graph {
	steps := make([]graphStep, 0, 2)
	if direction == "out" || direction == "both" {
		steps = append(steps, graphStep{
			neighbors: func(pkg string) []string {
				if info := idx.Packages[pkg]; info != nil {
					return info.Imports
				}
				return nil
			},
			edge: func(cur, next string) Edge {
				return Edge{From: idx.RelPath(cur), To: idx.RelPath(next)}
			},
		})
	}
	if direction == "in" || direction == "both" {
		steps = append(steps, graphStep{
			neighbors: func(pkg string) []string { return idx.RevImports[pkg] },
			edge: func(cur, next string) Edge {
				return Edge{From: idx.RelPath(next), To: idx.RelPath(cur)}
			},
		})
	}
	graph := walkGraph(rootPkg, depth, 0, steps)
	graph.Root = idx.RelPath(rootPkg)
	return graph
}

func walkGraph(root string, depth, limit int, steps []graphStep) Graph {
	seen := map[string]int{root: 0}
	queue := []string{root}
	edges := make([]Edge, 0)
	seenEdges := map[Edge]struct{}{}

	for len(queue) > 0 && (limit <= 0 || len(seen) < limit) {
		cur := queue[0]
		queue = queue[1:]
		d := seen[cur]
		if d >= depth {
			continue
		}
		for _, step := range steps {
			for _, next := range step.neighbors(cur) {
				edges = appendEdge(edges, seenEdges, step.edge(cur, next))
				if _, exists := seen[next]; !exists && (limit <= 0 || len(seen) < limit) {
					seen[next] = d + 1
					queue = append(queue, next)
				}
			}
		}
	}
	return Graph{Root: root, Nodes: len(seen), Edges: edges}
}

func appendEdge(edges []Edge, seen map[Edge]struct{}, edge Edge) []Edge {
	if _, ok := seen[edge]; ok {
		return edges
	}
	seen[edge] = struct{}{}
	return append(edges, edge)
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

package index

import (
	"fmt"
	"sort"
	"strings"
)

const (
	callPathDefaultDepth = 8
	callPathMaxDepth     = 20
	callPathDefaultPaths = 3
	callPathMaxPaths     = 10
)

// CallPathNode is one symbol visited by a call path.
type CallPathNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// CallPathHop is one edge between consecutive nodes of a call path.
type CallPathHop struct {
	From         CallPathNode `json:"from"`
	To           CallPathNode `json:"to"`
	EdgeKind     string       `json:"edge_kind"`
	ViaInterface string       `json:"via_interface,omitempty"`
}

// CallPath is one ordered call chain. Depth is the number of hops, so a
// self-target path has depth zero and no hops.
type CallPath struct {
	Nodes []CallPathNode `json:"nodes"`
	Hops  []CallPathHop  `json:"hops"`
	Depth int            `json:"depth"`
}

// CallPathResult is the deterministic shortest-path projection between two
// callable symbols. When From and To are the same symbol, Found is true and
// Paths contains one zero-hop path containing only that symbol.
type CallPathResult struct {
	From                     CallPathNode `json:"from"`
	To                       CallPathNode `json:"to"`
	Found                    bool         `json:"found"`
	Paths                    []CallPath   `json:"paths"`
	MaxDepth                 int          `json:"max_depth"`
	MaxPaths                 int          `json:"max_paths"`
	IncludeInterfaceDispatch bool         `json:"include_interface_dispatch"`
	ReversePathExists        bool         `json:"reverse_path_exists"`
	ReversePathHint          string       `json:"reverse_path_hint,omitempty"`
}

// CallPathEndpointError reports a missing or non-callable path endpoint.
type CallPathEndpointError struct {
	Role  string
	Input string
	Kind  string
}

func (e *CallPathEndpointError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("call path %s endpoint %q has kind %q; expected func or method", e.Role, e.Input, e.Kind)
	}
	return fmt.Sprintf("call path %s endpoint not found: %s", e.Role, e.Input)
}

type callPathEdge struct {
	to   string
	kind string
	via  string
}

// FindCallPath returns the deterministic shortest call chains from a source
// function or method to a target within maxDepth, up to maxPaths. Neighbors are
// ordered by symbol ID, so repeated calls return the same paths in the same
// order. maxDepth defaults to 8 (max 20); maxPaths defaults to 3 (max 10).
//
// The search uses the same Callees graph as get_call_graph and can include
// direct, interface_dispatch, and func_value edges. With
// includeInterfaceDispatch set to false, interface_dispatch edges are skipped.
// Cycles cannot loop because every hop advances one BFS level.
func (idx *Index) FindCallPath(fromID, toID string, maxDepth, maxPaths int, includeInterfaceDispatch bool) (CallPathResult, error) {
	maxDepth = clampCallPathDepth(maxDepth)
	maxPaths = clampCallPathPaths(maxPaths)

	fromSym, err := idx.callPathEndpoint("from", fromID)
	if err != nil {
		return CallPathResult{}, err
	}
	toSym, err := idx.callPathEndpoint("to", toID)
	if err != nil {
		return CallPathResult{}, err
	}

	result := CallPathResult{
		From:                     callPathNodeFor(fromSym),
		To:                       callPathNodeFor(toSym),
		Paths:                    []CallPath{},
		MaxDepth:                 maxDepth,
		MaxPaths:                 maxPaths,
		IncludeInterfaceDispatch: includeInterfaceDispatch,
	}

	if fromSym.ID == toSym.ID {
		result.Found = true
		result.Paths = append(result.Paths, CallPath{
			Nodes: []CallPathNode{callPathNodeFor(fromSym)},
			Hops:  []CallPathHop{},
		})
		return result, nil
	}

	distFrom := idx.callPathDistances(fromSym.ID, maxDepth, includeInterfaceDispatch)
	if shortest, ok := distFrom[toSym.ID]; ok {
		distTo := idx.callPathReverseDistances(toSym.ID, shortest, includeInterfaceDispatch)
		result.Paths = idx.enumerateCallPaths(fromSym.ID, toSym.ID, shortest, distFrom, distTo, maxPaths, includeInterfaceDispatch)
		result.Found = len(result.Paths) > 0
	}

	if !result.Found {
		if _, ok := idx.callPathDistances(toSym.ID, maxDepth, includeInterfaceDispatch)[fromSym.ID]; ok {
			result.ReversePathExists = true
			result.ReversePathHint = fmt.Sprintf(
				"no call path from %s to %s within depth %d; the reverse path %s -> %s exists",
				fromSym.ID, toSym.ID, maxDepth, toSym.ID, fromSym.ID,
			)
		}
	}
	return result, nil
}

func clampCallPathDepth(depth int) int {
	if depth <= 0 {
		return callPathDefaultDepth
	}
	if depth > callPathMaxDepth {
		return callPathMaxDepth
	}
	return depth
}

func clampCallPathPaths(paths int) int {
	if paths <= 0 {
		return callPathDefaultPaths
	}
	if paths > callPathMaxPaths {
		return callPathMaxPaths
	}
	return paths
}

func (idx *Index) callPathEndpoint(role, input string) (*Symbol, error) {
	id := strings.TrimSpace(input)
	if id == "" {
		return nil, &CallPathEndpointError{Role: role, Input: input}
	}
	sym := idx.Symbols[id]
	if sym == nil {
		sym = idx.CanonicalSymbols[id]
	}
	if sym == nil {
		return nil, &CallPathEndpointError{Role: role, Input: id}
	}
	if sym.Kind != "func" && sym.Kind != "method" {
		return nil, &CallPathEndpointError{Role: role, Input: id, Kind: sym.Kind}
	}
	return sym, nil
}

func callPathNodeFor(sym *Symbol) CallPathNode {
	if sym == nil {
		return CallPathNode{}
	}
	return CallPathNode{
		ID:   sym.ID,
		Name: sym.Name,
		Kind: sym.Kind,
		File: sym.File,
		Line: sym.Line,
	}
}

func (idx *Index) callPathNode(id string) CallPathNode {
	if sym := idx.Symbols[id]; sym != nil {
		return callPathNodeFor(sym)
	}
	return CallPathNode{ID: id}
}

// callPathEdges returns one node's outgoing call edges in stable order.
func (idx *Index) callPathEdges(id string, includeInterfaceDispatch bool) []callPathEdge {
	neighbors := idx.filterCallees(id, includeInterfaceDispatch)
	out := make([]callPathEdge, 0, len(neighbors))
	for _, next := range neighbors {
		edge := idx.callEdge(id, next)
		kind := edge.Kind
		if kind == "" {
			kind = "direct"
		}
		out = append(out, callPathEdge{to: next, kind: kind, via: edge.Via})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].to != out[j].to {
			return out[i].to < out[j].to
		}
		if out[i].kind != out[j].kind {
			return out[i].kind < out[j].kind
		}
		return out[i].via < out[j].via
	})
	return out
}

func (idx *Index) callPathCallers(id string, includeInterfaceDispatch bool) []string {
	callers := append([]string(nil), idx.filterCallers(id, includeInterfaceDispatch)...)
	sort.Strings(callers)
	return callers
}

// callPathDistances computes shortest forward hop counts from start, bounded
// by maxDepth.
func (idx *Index) callPathDistances(start string, maxDepth int, includeInterfaceDispatch bool) map[string]int {
	dist := map[string]int{start: 0}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		depth := dist[cur]
		if depth >= maxDepth {
			continue
		}
		for _, edge := range idx.callPathEdges(cur, includeInterfaceDispatch) {
			if _, seen := dist[edge.to]; seen {
				continue
			}
			dist[edge.to] = depth + 1
			queue = append(queue, edge.to)
		}
	}
	return dist
}

// callPathReverseDistances computes shortest hop counts from each node to end,
// bounded by maxDepth. It walks Callers so shortest-path pruning in
// enumerateCallPaths stays direction-correct.
func (idx *Index) callPathReverseDistances(end string, maxDepth int, includeInterfaceDispatch bool) map[string]int {
	dist := map[string]int{end: 0}
	queue := []string{end}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		depth := dist[cur]
		if depth >= maxDepth {
			continue
		}
		for _, caller := range idx.callPathCallers(cur, includeInterfaceDispatch) {
			if _, seen := dist[caller]; seen {
				continue
			}
			dist[caller] = depth + 1
			queue = append(queue, caller)
		}
	}
	return dist
}

// enumerateCallPaths walks only edges that lie on a shortest path, so cycles
// and dead-end branches cannot expand the search. Neighbor order comes from
// callPathEdges, keeping returned paths stable across runs.
func (idx *Index) enumerateCallPaths(from, to string, shortest int, distFrom, distTo map[string]int, maxPaths int, includeInterfaceDispatch bool) []CallPath {
	paths := make([]CallPath, 0, maxPaths)
	nodes := []CallPathNode{idx.callPathNode(from)}
	hops := make([]CallPathHop, 0, shortest)

	var walk func(cur string)
	walk = func(cur string) {
		if len(paths) >= maxPaths {
			return
		}
		if cur == to {
			pathNodes := append([]CallPathNode(nil), nodes...)
			pathHops := append([]CallPathHop(nil), hops...)
			paths = append(paths, CallPath{Nodes: pathNodes, Hops: pathHops, Depth: len(pathHops)})
			return
		}

		curDepth := distFrom[cur]
		for _, edge := range idx.callPathEdges(cur, includeInterfaceDispatch) {
			if distFrom[edge.to] != curDepth+1 {
				continue
			}
			remaining, ok := distTo[edge.to]
			if !ok || curDepth+1+remaining != shortest {
				continue
			}
			fromNode := nodes[len(nodes)-1]
			toNode := idx.callPathNode(edge.to)
			nodes = append(nodes, toNode)
			hops = append(hops, CallPathHop{
				From: fromNode, To: toNode, EdgeKind: edge.kind, ViaInterface: edge.via,
			})
			walk(edge.to)
			nodes = nodes[:len(nodes)-1]
			hops = hops[:len(hops)-1]
			if len(paths) >= maxPaths {
				return
			}
		}
	}
	walk(from)
	return paths
}

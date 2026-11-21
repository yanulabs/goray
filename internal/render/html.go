package render

import (
	"bytes"
	"embed"
	"encoding/json"
	"sort"
	"text/template"
)

//go:embed html/template.html html/app.js html/styles.css
var htmlAssets embed.FS

var htmlTemplate = template.Must(template.New("report").Parse(mustHTMLAsset("html/template.html")))

func mustHTMLAsset(name string) string {
	data, err := htmlAssets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// HTMLReport is the self-contained data payload rendered into the HTML report.
type HTMLReport struct {
	Repo            string            `json:"repo"`
	ModulePath      string            `json:"module_path"`
	GeneratedAt     string            `json:"generated_at"`
	Metrics         HTMLMetrics       `json:"metrics"`
	Coverage        HTMLCoverage      `json:"coverage"`
	Hotspots        []HTMLSymbol      `json:"hotspots"`
	CoupledPackages []HTMLCoupled     `json:"coupled_packages"`
	Modules         []HTMLModuleCard  `json:"modules"`
	Packages        []HTMLPackageCard `json:"packages"`
	Symbols         []HTMLSymbol      `json:"symbols"`
	Interfaces      []HTMLInterface   `json:"interfaces"`
	Graphs          HTMLGraphs        `json:"graphs"`
	Tree            []*HTMLTreeNode   `json:"tree"`
}

type HTMLMetrics struct {
	Modules  int `json:"modules"`
	Packages int `json:"packages"`
	Files    int `json:"files"`
	Types    int `json:"types"`
	Funcs    int `json:"funcs"`
	Methods  int `json:"methods"`
	LOC      int `json:"loc"`
	Dead     int `json:"dead"`
}

type HTMLCoverage struct {
	Eligible    int     `json:"eligible"`
	Synced      int     `json:"synced"`
	SoftStale   int     `json:"soft_stale"`
	HardStale   int     `json:"hard_stale"`
	Missing     int     `json:"missing"`
	NotEligible int     `json:"not_eligible"`
	Percent     float64 `json:"percent"`
}

type HTMLSummary struct {
	Intent        string   `json:"intent,omitempty"`
	SideEffects   []string `json:"side_effects,omitempty"`
	Invariants    []string `json:"invariants,omitempty"`
	Concurrency   string   `json:"concurrency,omitempty"`
	KeyInvariants []string `json:"key_invariants,omitempty"`
	Boundaries    []string `json:"boundaries,omitempty"`
	SyncState     string   `json:"sync_state"`
	ModelID       string   `json:"model_id,omitempty"`
	DepsChanged   bool     `json:"deps_changed,omitempty"`
}

type HTMLSymbol struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	PkgPath     string       `json:"pkg_path,omitempty"`
	Module      string       `json:"module,omitempty"`
	File        string       `json:"file,omitempty"`
	StartLine   int          `json:"start_line,omitempty"`
	EndLine     int          `json:"end_line,omitempty"`
	LOC         int          `json:"loc"`
	Complexity  int          `json:"complexity"`
	FanIn       int          `json:"fan_in"`
	FanOut      int          `json:"fan_out"`
	Exported    bool         `json:"exported"`
	Dead        bool         `json:"dead"`
	Eligible    bool         `json:"eligible"`
	IsTest      bool         `json:"is_test"`
	IsGenerated bool         `json:"is_generated"`
	SyncState   string       `json:"sync_state"`
	Summary     *HTMLSummary `json:"summary,omitempty"`
	Callers     []string     `json:"callers,omitempty"`
	Callees     []string     `json:"callees,omitempty"`
}

type HTMLCoupled struct {
	Pkg    string `json:"pkg"`
	FanIn  int    `json:"fan_in"`
	FanOut int    `json:"fan_out"`
}

type HTMLModuleCard struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Packages     int          `json:"packages"`
	ProdLOC      int          `json:"prod_loc"`
	TestLOC      int          `json:"test_loc"`
	RootExported int          `json:"root_exported"`
	PortsCalls   int          `json:"ports_calls"`
	Goroutines   int          `json:"goroutines"`
	OutModules   int          `json:"out_modules"`
	InModules    int          `json:"in_modules"`
	SyncState    string       `json:"sync_state"`
	Summary      *HTMLSummary `json:"summary,omitempty"`
}

type HTMLPackageCard struct {
	ID        string       `json:"id"`
	PkgPath   string       `json:"pkg_path"`
	RelPath   string       `json:"rel_path"`
	Module    string       `json:"module"`
	ProdLOC   int          `json:"prod_loc"`
	TestLOC   int          `json:"test_loc"`
	Exported  int          `json:"exported"`
	FanIn     int          `json:"fan_in"`
	FanOut    int          `json:"fan_out"`
	SyncState string       `json:"sync_state"`
	Summary   *HTMLSummary `json:"summary,omitempty"`
}

type HTMLInterface struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	PkgPath         string     `json:"pkg_path"`
	Methods         int        `json:"methods"`
	Implementations []HTMLImpl `json:"implementations"`
}

type HTMLImpl struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	PkgPath string `json:"pkg_path"`
	ViaPtr  bool   `json:"via_ptr"`
}

type HTMLGraphs struct {
	Modules  HTMLGraph `json:"modules"`
	Packages HTMLGraph `json:"packages"`
}

type HTMLGraph struct {
	Nodes []HTMLGraphNode `json:"nodes"`
	Edges []HTMLGraphEdge `json:"edges"`
}

type HTMLGraphNode struct {
	ID        string       `json:"id"`
	Label     string       `json:"label"`
	Kind      string       `json:"kind"`
	Module    string       `json:"module,omitempty"`
	LOC       int          `json:"loc"`
	FanIn     int          `json:"fan_in"`
	FanOut    int          `json:"fan_out"`
	Eligible  bool         `json:"eligible"`
	Layer     int          `json:"layer"`
	Order     int          `json:"order"`
	SyncState string       `json:"sync_state"`
	Summary   *HTMLSummary `json:"summary,omitempty"`
}

type HTMLGraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
}

type HTMLTreeNode struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Label     string          `json:"label"`
	SyncState string          `json:"sync_state"`
	Children  []*HTMLTreeNode `json:"children,omitempty"`
}

// RenderHTML renders the single-file offline report.
func RenderHTML(report HTMLReport) (string, error) {
	// Hotspots are a derived production-risk view; never trust stale or noisy input.
	report.Hotspots = BuildHotspots(report.Symbols)
	payload, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	data := struct {
		CSS        string
		JS         string
		ReportJSON string
	}{
		CSS:        mustHTMLAsset("html/styles.css"),
		JS:         mustHTMLAsset("html/app.js"),
		ReportJSON: string(payload),
	}
	var out bytes.Buffer
	if err := htmlTemplate.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

// BuildHotspots selects the highest-risk production symbols. Test and generated
// declarations are excluded because their branches are not shipped risk.
func BuildHotspots(symbols []HTMLSymbol) []HTMLSymbol {
	hotspots := make([]HTMLSymbol, 0)
	for _, symbol := range symbols {
		if symbol.IsTest || symbol.IsGenerated {
			continue
		}
		switch symbol.Kind {
		case "func", "method", "type":
		default:
			continue
		}
		if symbol.Complexity == 0 && symbol.FanIn == 0 {
			continue
		}
		hotspots = append(hotspots, symbol)
	}
	sort.Slice(hotspots, func(i, j int) bool {
		left := hotspots[i].Complexity*2 + hotspots[i].FanIn
		right := hotspots[j].Complexity*2 + hotspots[j].FanIn
		if left != right {
			return left > right
		}
		return hotspots[i].ID < hotspots[j].ID
	})
	if len(hotspots) > 10 {
		hotspots = hotspots[:10]
	}
	return hotspots
}

// LayoutGraph assigns deterministic layer/order coordinates, breaking cycles
// lexically when no acyclic frontier remains.
func LayoutGraph(graph HTMLGraph) HTMLGraph {
	nodes := append([]HTMLGraphNode(nil), graph.Nodes...)
	edges := uniqueGraphEdges(graph.Edges)
	index := make(map[string]int, len(nodes))
	for i := range nodes {
		index[nodes[i].ID] = i
	}
	adjacency := make([][]int, len(nodes))
	indegree := make([]int, len(nodes))
	for _, edge := range edges {
		from, okFrom := index[edge.From]
		to, okTo := index[edge.To]
		if !okFrom || !okTo || from == to {
			continue
		}
		adjacency[from] = append(adjacency[from], to)
		indegree[to]++
	}

	layer := make([]int, len(nodes))
	processed := make([]bool, len(nodes))
	for processedCount := 0; processedCount < len(nodes); processedCount++ {
		candidates := make([]int, 0)
		for i := range nodes {
			if !processed[i] && indegree[i] == 0 {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			for i := range nodes {
				if !processed[i] {
					candidates = append(candidates, i)
				}
			}
		}
		sortNodeCandidates(nodes, candidates)
		current := candidates[0]
		processed[current] = true
		for _, next := range adjacency[current] {
			if processed[next] {
				continue
			}
			if layer[next] < layer[current]+1 {
				layer[next] = layer[current] + 1
			}
			if indegree[next] > 0 {
				indegree[next]--
			}
		}
	}

	byLayer := make(map[int][]int)
	for i := range nodes {
		nodes[i].Layer = layer[i]
		byLayer[layer[i]] = append(byLayer[layer[i]], i)
	}
	for _, indexes := range byLayer {
		sortNodeCandidates(nodes, indexes)
		for order, idx := range indexes {
			nodes[idx].Order = order
		}
	}
	return HTMLGraph{Nodes: nodes, Edges: edges}
}

func uniqueGraphEdges(edges []HTMLGraphEdge) []HTMLGraphEdge {
	seen := make(map[HTMLGraphEdge]struct{}, len(edges))
	out := make([]HTMLGraphEdge, 0, len(edges))
	for _, edge := range edges {
		if _, ok := seen[edge]; ok {
			continue
		}
		seen[edge] = struct{}{}
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

func sortNodeCandidates(nodes []HTMLGraphNode, indexes []int) {
	sort.Slice(indexes, func(i, j int) bool {
		a, b := nodes[indexes[i]], nodes[indexes[j]]
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
}

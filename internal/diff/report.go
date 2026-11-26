// Package diff compares two goray index snapshots and renders deterministic
// architectural deltas for the CLI and MCP server.
package diff

// Report is the JSON/markdown diff payload. Field names and tags are part of
// the public CLI contract and must stay stable.
type Report struct {
	BaseRef    string   `json:"base_ref"`
	BaseCommit string   `json:"base_commit"`
	HeadRef    string   `json:"head_ref"`
	Head       string   `json:"head"`
	Summary    Counters `json:"summary"`
	Cycles     Cycles   `json:"cycles"`
	API        API      `json:"breaking_api"`
	Hotspots   Hotspots `json:"hotspots"`
	Dead       DeadCode `json:"dead_code"`
	Failed     []string `json:"failed_gates,omitempty"`
}

// Counters reports package, file, LOC, symbol, module-edge, and hotspot totals.
type Counters struct {
	Packages         int `json:"packages"`
	PackagesBase     int `json:"packages_base"`
	Files            int `json:"files"`
	FilesBase        int `json:"files_base"`
	LOC              int `json:"loc"`
	LOCBase          int `json:"loc_base"`
	Symbols          int `json:"symbols"`
	SymbolsBase      int `json:"symbols_base"`
	ModuleEdges      int `json:"module_edges"`
	ModuleEdgesBase  int `json:"module_edges_base"`
	UntestedHotspots int `json:"untested_hotspots"`
}

// Cycles reports added and resolved cross-module cycles.
type Cycles struct {
	New      []string `json:"new"`
	Resolved []string `json:"resolved"`
}

// API reports exported API additions, modifications, and removals.
type API struct {
	Removed  []APIChange `json:"removed"`
	Modified []APIChange `json:"modified"`
	Added    []APIChange `json:"added"`
}

// APIChange is one exported API symbol delta.
type APIChange struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Hotspots reports newly untested complex functions and the largest regressions.
type Hotspots struct {
	New      []Hotspot `json:"new"`
	TopDelta []Hotspot `json:"top_delta"`
}

// Hotspot is one complexity/test-coverage delta.
type Hotspot struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Module       string `json:"module"`
	BeforeLOC    int    `json:"before_loc,omitempty"`
	AfterLOC     int    `json:"after_loc"`
	BeforeCC     int    `json:"before_complexity,omitempty"`
	AfterCC      int    `json:"after_complexity"`
	DeltaLOC     int    `json:"delta_loc"`
	DeltaCC      int    `json:"delta_complexity"`
	TestCovered  bool   `json:"test_covered"`
	CoverageLost bool   `json:"coverage_lost,omitempty"`
}

// DeadCode reports newly dead and newly removed dead symbols.
type DeadCode struct {
	New     []DeadItem `json:"new"`
	Removed []DeadItem `json:"removed"`
}

// DeadItem is one dead-code symbol.
type DeadItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Module string `json:"module"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

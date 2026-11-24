package main

type diffReport struct {
	BaseRef    string       `json:"base_ref"`
	BaseCommit string       `json:"base_commit"`
	HeadRef    string       `json:"head_ref"`
	Head       string       `json:"head"`
	Summary    diffCounters `json:"summary"`
	Cycles     diffCycles   `json:"cycles"`
	API        diffAPI      `json:"breaking_api"`
	Hotspots   diffHotspots `json:"hotspots"`
	Dead       diffDeadCode `json:"dead_code"`
	Failed     []string     `json:"failed_gates,omitempty"`
}

type diffCounters struct {
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

type diffCycles struct {
	New      []string `json:"new"`
	Resolved []string `json:"resolved"`
}

type diffAPI struct {
	Removed  []diffAPIChange `json:"removed"`
	Modified []diffAPIChange `json:"modified"`
	Added    []diffAPIChange `json:"added"`
}

type diffAPIChange struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type diffHotspots struct {
	New      []diffHotspot `json:"new"`
	TopDelta []diffHotspot `json:"top_delta"`
}

type diffHotspot struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Module      string `json:"module"`
	BeforeLOC   int    `json:"before_loc,omitempty"`
	AfterLOC    int    `json:"after_loc"`
	BeforeCC    int    `json:"before_complexity,omitempty"`
	AfterCC     int    `json:"after_complexity"`
	DeltaLOC    int    `json:"delta_loc"`
	DeltaCC     int    `json:"delta_complexity"`
	TestCovered bool   `json:"test_covered"`
}

type diffDeadCode struct {
	New     []diffDeadItem `json:"new"`
	Removed []diffDeadItem `json:"removed"`
}

type diffDeadItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Module string `json:"module"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

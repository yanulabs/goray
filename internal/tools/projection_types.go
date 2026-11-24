package tools

import "github.com/yanulabs/goray/internal/index"

type generatedOmitted struct {
	Files   int `json:"files"`
	Symbols int `json:"symbols"`
	LOC     int `json:"loc"`
}

type compactModule struct {
	Name             string `json:"name"`
	Packages         int    `json:"packages"`
	Files            int    `json:"files"`
	GeneratedFiles   int    `json:"generated_files"`
	GeneratedSymbols int    `json:"generated_symbols"`
	GeneratedLOC     int    `json:"generated_loc"`
	UntrackedFiles   int    `json:"untracked_files"`
	ProdLOC          int    `json:"prod_loc"`
	TestLOC          int    `json:"test_loc"`
	TestOnly         bool   `json:"test_only,omitempty"`
	AllUntracked     bool   `json:"all_untracked,omitempty"`
	RootExported     int    `json:"root_exported"`
	PortsCalls       int    `json:"ports_calls"`
	Goroutines       int    `json:"goroutines"`
	FanIn            int    `json:"fan_in"`
	FanOut           int    `json:"fan_out"`
}

type compactEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Symbols int    `json:"symbols"`
}

type compactCodebase struct {
	ModulePath       string             `json:"module_path"`
	Packages         int                `json:"packages"`
	ModuleCount      int                `json:"module_count"`
	Symbols          int                `json:"symbols"`
	GeneratedFiles   int                `json:"generated_files"`
	GeneratedSymbols int                `json:"generated_symbols"`
	GeneratedLOC     int                `json:"generated_loc"`
	UntrackedFiles   int                `json:"untracked_files"`
	Detail           string             `json:"detail"`
	SummaryOnly      bool               `json:"summary_only"`
	SortBy           string             `json:"sort_by"`
	Modules          []compactModule    `json:"modules"`
	TotalModules     int                `json:"total_modules"`
	OmittedModules   int                `json:"omitted_modules"`
	OmittedCount     int                `json:"omitted_count"`
	CrossModuleEdges []compactEdge      `json:"cross_module_edges,omitempty"`
	ModuleCycles     []string           `json:"module_cycles,omitempty"`
	LargeFiles       []index.FileMetric `json:"large_files,omitempty"`
	TotalLargeFiles  int                `json:"total_large_files,omitempty"`
	TopHotspots      []index.Hotspot    `json:"top_hotspots,omitempty"`
	TotalHotspots    int                `json:"total_hotspots,omitempty"`
	Truncated        bool               `json:"truncated"`
	TotalCount       int                `json:"total_count"`
	ReturnedCount    int                `json:"returned_count"`
	Hint             string             `json:"hint,omitempty"`
	IndexState       index.State        `json:"index_state"`
}

type compactPackage struct {
	PkgPath          string `json:"pkg_path"`
	RelPath          string `json:"rel_path"`
	Name             string `json:"name"`
	ProdFiles        int    `json:"prod_files"`
	Files            int    `json:"files"`
	TestFiles        int    `json:"test_files"`
	ProdLOC          int    `json:"prod_loc"`
	TestLOC          int    `json:"test_loc"`
	Exported         int    `json:"exported"`
	PortsCalls       int    `json:"ports_calls"`
	Goroutines       int    `json:"goroutines"`
	GeneratedFiles   int    `json:"generated_files"`
	GeneratedSymbols int    `json:"generated_symbols"`
	GeneratedLOC     int    `json:"generated_loc"`
	UntrackedFiles   int    `json:"untracked_files"`
	TestOnly         bool   `json:"test_only,omitempty"`
}

type moduleEdgeCount struct {
	Module  string `json:"module"`
	Symbols int    `json:"symbols"`
}

type dependencyEdges struct {
	Out []moduleEdgeCount `json:"out"`
	In  []moduleEdgeCount `json:"in"`
}

type compactModuleOverview struct {
	Module           compactModule      `json:"module"`
	PackageRollups   []compactPackage   `json:"package_rollups"`
	Subpackages      []compactPackage   `json:"subpackages"`
	TotalPackages    int                `json:"total_packages"`
	DependencyEdges  dependencyEdges    `json:"dependency_edges"`
	TopHotspots      []index.Hotspot    `json:"top_hotspots"`
	TotalHotspots    int                `json:"total_hotspots"`
	LargeFiles       []index.FileMetric `json:"large_files,omitempty"`
	TotalLargeFiles  int                `json:"total_large_files"`
	GeneratedOmitted *generatedOmitted  `json:"generated_omitted,omitempty"`
	IncludeGenerated bool               `json:"include_generated"`
	Truncated        bool               `json:"truncated"`
	TotalCount       int                `json:"total_count"`
	ReturnedCount    int                `json:"returned_count"`
	Hint             string             `json:"hint,omitempty"`
	IndexState       index.State        `json:"index_state"`
}

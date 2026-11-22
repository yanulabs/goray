package cache

import "encoding/json"

// Dependency is one summary-context dependency link.
type Dependency struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	SyncState SyncState       `json:"sync_state"`
	Exported  bool            `json:"is_exported"`
	Eligible  bool            `json:"is_eligible"`
	Summary   json.RawMessage `json:"summary,omitempty"`
	Source    string          `json:"source,omitempty"`
}

// SummaryContext is the source, schema, and dependency payload for a summary target.
type SummaryContext struct {
	Symbol              WorkingSymbol   `json:"symbol"`
	SyncState           SyncState       `json:"sync_state"`
	PromptVersion       string          `json:"prompt_version"`
	Schema              map[string]any  `json:"schema"`
	SelfHash            string          `json:"self_hash"`
	DepHash             string          `json:"dep_hash"`
	ContentHash         string          `json:"content_hash"`
	DepsChanged         bool            `json:"deps_changed,omitempty"`
	Reason              string          `json:"reason,omitempty"`
	ChangedDependencies []string        `json:"changed_dependencies,omitempty"`
	PreviousSummary     json.RawMessage `json:"previous_summary,omitempty"`
	Source              string          `json:"source,omitempty"`
	Doc                 string          `json:"doc,omitempty"`
	Signature           string          `json:"signature,omitempty"`
	Directives          []string        `json:"directives,omitempty"`
	Dependencies        []Dependency    `json:"dependencies,omitempty"`
	Note                string          `json:"note,omitempty"`

	ParentPackageID            string    `json:"parent_package_id,omitempty"`
	ParentModuleID             string    `json:"parent_module_id,omitempty"`
	ParentPackageSummaryCached *bool     `json:"parent_package_summary_cached,omitempty"`
	ParentModuleSummaryCached  *bool     `json:"parent_module_summary_cached,omitempty"`
	ParentPackageSyncState     SyncState `json:"parent_package_sync_state,omitempty"`
	ParentModuleSyncState      SyncState `json:"parent_module_sync_state,omitempty"`

	Module *ModuleContext `json:"module_context,omitempty"`
}

// ModuleContext is the compact architectural context returned for mod: targets.
type ModuleContext struct {
	Name                   string              `json:"name"`
	ProdLOC                int                 `json:"prod_loc"`
	TestLOC                int                 `json:"test_loc"`
	Packages               int                 `json:"packages"`
	Files                  int                 `json:"files"`
	PackageRollups         []ModulePackage     `json:"package_rollups"`
	DependencyEdges        ModuleEdges         `json:"dependency_edges"`
	ExportedAPI            []ModuleAPI         `json:"exported_api,omitempty"`
	KeyFiles               []ModuleFileExcerpt `json:"key_files,omitempty"`
	CachedPackageSummaries []string            `json:"cached_package_summaries,omitempty"`
	Truncated              bool                `json:"truncated"`
}

// ModulePackage is one package rollup inside a module summary context.
type ModulePackage struct {
	ID        string   `json:"id"`
	PkgPath   string   `json:"pkg_path"`
	RelPath   string   `json:"rel_path"`
	Name      string   `json:"name"`
	ProdFiles int      `json:"prod_files"`
	Files     int      `json:"files"`
	ProdLOC   int      `json:"prod_loc"`
	TestLOC   int      `json:"test_loc"`
	Exported  int      `json:"exported"`
	TestOnly  bool     `json:"test_only,omitempty"`
	Imports   []string `json:"imports,omitempty"`
	Doc       string   `json:"doc,omitempty"`
}

// ModuleEdge is one incoming or outgoing module dependency edge.
type ModuleEdge struct {
	Module  string `json:"module"`
	Symbols int    `json:"symbols"`
}

// ModuleEdges is the module-level dependency edge projection.
type ModuleEdges struct {
	Out []ModuleEdge `json:"out"`
	In  []ModuleEdge `json:"in"`
}

// ModuleAPI is one exported declaration inside a module.
type ModuleAPI struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	PkgPath   string `json:"pkg_path"`
	Signature string `json:"signature,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
}

// ModuleFileExcerpt is a bounded excerpt of one key production file.
type ModuleFileExcerpt struct {
	Path      string `json:"path"`
	LOC       int    `json:"loc"`
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated,omitempty"`
}

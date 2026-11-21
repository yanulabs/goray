// Package index builds and queries a semantic index of a Go module.
package index

import (
	"sort"
	"time"
)

// Symbol is a package-level declaration or a method.
type Symbol struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // func, method, interface, struct, type, var, const
	PkgPath    string `json:"pkg_path"`
	PkgRel     string `json:"pkg_rel"`
	ModuleArea string `json:"module_area"`
	RecvType   string `json:"recv_type,omitempty"`
	RecvPtr    bool   `json:"-"`
	Methods    int    `json:"-"`
	Fields     int    `json:"fields,omitempty"`
	Exported   bool   `json:"exported"`
	Signature  string `json:"signature"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	EndLine    int    `json:"end_line,omitempty"`
	LOC        int    `json:"loc,omitempty"`
	Cyclomatic int    `json:"cyclomatic,omitempty"`

	// Provenance flags. Generated and Untracked are exposed by audit tools;
	// IsTest and IsHook are used to suppress false positives but hidden from
	// default JSON output.
	Generated bool `json:"generated,omitempty"`
	Untracked bool `json:"untracked,omitempty"`
	IsTest    bool `json:"-"`
	IsHook    bool `json:"-"`
}

// XRef is one type-checked use of a symbol.
type XRef struct {
	TargetID   string `json:"target_id"`
	FromPkg    string `json:"from_pkg"`
	FromMod    string `json:"from_mod,omitempty"`
	FromFuncID string `json:"from_func_id,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
}

// ImplMatch records an interface/type satisfaction edge.
type ImplMatch struct {
	IfaceID   string `json:"iface_id"`
	TypeID    string `json:"type_id"`
	ViaPtr    bool   `json:"via_ptr"`
	TypeFile  string `json:"type_file"`
	TypeLine  int    `json:"type_line"`
	IfaceFile string `json:"iface_file"`
	IfaceLine int    `json:"iface_line"`
}

type FileMetric struct {
	Path      string `json:"path"`
	LOC       int    `json:"loc"`
	Generated bool   `json:"generated,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`
}

// PkgInfo is the architecture-level summary of one package.
type PkgInfo struct {
	PkgPath    string   `json:"pkg_path"`
	RelPath    string   `json:"rel_path"`
	Name       string   `json:"name"`
	Dir        string   `json:"dir"`
	ModuleArea string   `json:"module_area"`
	Imports    []string `json:"imports"` // intra-module only
	ProdFiles  int      `json:"prod_files"`
	Files      int      `json:"files"`
	TestFiles  int      `json:"test_files"`
	ProdLOC    int      `json:"prod_loc"`
	TestLOC    int      `json:"test_loc"`
	Exported   int      `json:"exported"`
	PortsCalls int      `json:"ports_calls"`
	Goroutines int      `json:"goroutines"`

	GeneratedFiles int `json:"generated_files,omitempty"`
	UntrackedFiles int `json:"untracked_files,omitempty"`
}

// ModSummary aggregates the packages under modules/<name>.
type ModSummary struct {
	Name         string              `json:"name"`
	Packages     int                 `json:"packages"`
	Files        int                 `json:"files"`
	GeneratedFiles int              `json:"generated_files"`
	UntrackedFiles int              `json:"untracked_files"`
	AllUntracked bool                `json:"all_untracked,omitempty"`
	ProdLOC      int                 `json:"prod_loc"`
	TestLOC      int                 `json:"test_loc"`
	RootExported int                 `json:"root_exported"`
	PortsCalls   int                 `json:"ports_calls"`
	Goroutines   int                 `json:"goroutines"`
	OutModules   map[string][]string `json:"out_modules"`
	InModules    map[string][]string `json:"in_modules"`
	LargeFiles   []FileMetric        `json:"large_files,omitempty"`
}

// Index is an immutable snapshot after Build returns.
type Index struct {
	RootDir        string
	ModulePath     string
	BuiltAt        time.Time
	BuildDuration  time.Duration
	LoadErrors     []string
	TestLoadErrors []string

	Packages         map[string]*PkgInfo
	RevImports       map[string][]string
	Symbols          map[string]*Symbol
	SymbolsByName    map[string][]*Symbol
	CanonicalSymbols map[string]*Symbol
	XRefs            map[string][]XRef
	Callers          map[string][]string
	Callees          map[string][]string
	IfaceImpls       map[string][]ImplMatch
	TypeImpls        map[string][]ImplMatch
	IfaceMethods     map[string]bool
	Modules          map[string]*ModSummary
	LargeFiles       []FileMetric
	Summary          *SummaryData

	// TestCovered holds production functions reachable from any *_test.go
	// function through the indexed call graph. It is computed at build time.
	TestCovered map[string]bool
	// UntrackedFiles holds relative paths reported as untracked by git. The
	// index never excludes these files; callers decide whether to filter them.
	UntrackedFiles map[string]bool
}

// TotalXRefs returns the number of recorded references.
func (idx *Index) TotalXRefs() int {
	n := 0
	for _, refs := range idx.XRefs {
		n += len(refs)
	}
	return n
}

// SortedModuleNames returns module names in lexical order.
func (idx *Index) SortedModuleNames() []string {
	out := make([]string, 0, len(idx.Modules))
	for name := range idx.Modules {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModulesByLOC returns modules ordered by production size.
func (idx *Index) ModulesByLOC() []*ModSummary {
	out := make([]*ModSummary, 0, len(idx.Modules))
	for _, m := range idx.Modules {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProdLOC != out[j].ProdLOC {
			return out[i].ProdLOC > out[j].ProdLOC
		}
		return out[i].Name < out[j].Name
	})
	return out
}

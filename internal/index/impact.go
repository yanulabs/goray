package index

import (
	"sort"
	"strings"
)

// Impact is the blast radius of a change.
type Impact struct {
	Risk             string   `json:"risk"`
	Modules          []string `json:"impacted_modules"`
	Packages         int      `json:"impacted_pkgs"`
	Files            []string `json:"impacted_files"`
	Callers          []string `json:"impacted_callers"`
	TestPackages     []string `json:"test_packages"`
	SuggestedCommand string   `json:"suggested_cmd"`

	// Kind-aware blast radius. These fields are populated for every symbol
	// kind and are empty only when the category does not apply.
	Implementations   []ImpactSymbol  `json:"implementations,omitempty"`
	TestMocks         []ImpactSymbol  `json:"test_mocks,omitempty"`
	DirectReferences  []ImpactRef     `json:"direct_references,omitempty"`
	TransitiveCallers []string        `json:"transitive_callers,omitempty"`
	AffectedPackages  []string        `json:"affected_packages,omitempty"`
	TestPackagesToRun []string        `json:"test_packages_to_run,omitempty"`
	CompositeLiterals []CompositeSite `json:"composite_literals,omitempty"`
	UnkeyedLiterals   []CompositeSite `json:"unkeyed_literals,omitempty"`
}

// ImpactSymbol is one concrete symbol in the blast radius.
type ImpactSymbol struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	PkgPath string `json:"pkg_path"`
	PkgRel  string `json:"pkg_rel"`
	Module  string `json:"module,omitempty"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Test    bool   `json:"test"`
}

func newImpactSymbol(sym *Symbol) ImpactSymbol {
	return ImpactSymbol{
		ID: sym.ID, Name: sym.Name, Kind: sym.Kind, PkgPath: sym.PkgPath,
		PkgRel: sym.PkgRel, Module: sym.ModuleArea, File: sym.File, Line: sym.Line,
		Test: sym.IsTest,
	}
}

// ImpactRef is one grouped direct reference.
type ImpactRef struct {
	FromPkg    string `json:"from_pkg"`
	FromMod    string `json:"from_mod,omitempty"`
	FromFuncID string `json:"from_func_id,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Test       bool   `json:"test,omitempty"`
}

// ImpactBySymbol follows references and transitive callers, expanding
// interface implementations, struct composite literals, fields, and vars.
func (idx *Index) ImpactBySymbol(sym *Symbol, depth int) Impact {
	pkgs := map[string]struct{}{sym.PkgPath: {}}
	files := map[string]struct{}{sym.File: {}}
	mods := map[string]struct{}{}
	callers := map[string]struct{}{}
	if sym.ModuleArea != "" {
		mods[sym.ModuleArea] = struct{}{}
	}

	direct := make([]ImpactRef, 0, len(idx.XRefs[sym.ID]))
	testRefFiles := map[string]struct{}{}
	for _, ref := range idx.XRefs[sym.ID] {
		pkgs[ref.FromPkg] = struct{}{}
		files[ref.File] = struct{}{}
		if ref.FromMod != "" {
			mods[ref.FromMod] = struct{}{}
		}
		if ref.FromFuncID != "" {
			callers[ref.FromFuncID] = struct{}{}
		}
		if ref.Test {
			testRefFiles[ref.File] = struct{}{}
		}
		direct = append(direct, ImpactRef{
			FromPkg: ref.FromPkg, FromMod: ref.FromMod, FromFuncID: ref.FromFuncID,
			File: ref.File, Line: ref.Line, Test: ref.Test,
		})
	}

	impact := Impact{
		DirectReferences: direct,
		Callers:          nil,
	}
	roots := []string{sym.ID}

	// Include references to the struct's fields and methods for type targets.
	if sym.Kind == "struct" || sym.Kind == "type" || sym.Kind == "interface" {
		for _, other := range idx.Symbols {
			if other.PkgPath != sym.PkgPath || other.RecvType != sym.Name || other.ID == sym.ID {
				continue
			}
			for _, ref := range idx.XRefs[other.ID] {
				pkgs[ref.FromPkg] = struct{}{}
				files[ref.File] = struct{}{}
				if ref.FromMod != "" {
					mods[ref.FromMod] = struct{}{}
				}
				if ref.FromFuncID != "" {
					callers[ref.FromFuncID] = struct{}{}
				}
				direct = append(direct, ImpactRef{
					FromPkg: ref.FromPkg, FromMod: ref.FromMod, FromFuncID: ref.FromFuncID,
					File: ref.File, Line: ref.Line, Test: ref.Test,
				})
			}
		}
	}

	if sym.Kind == "interface" {
		for _, match := range idx.IfaceImpls[sym.ID] {
			impl := idx.Symbols[match.TypeID]
			if impl == nil {
				continue
			}
			entry := newImpactSymbol(impl)
			if impl.IsTest {
				impact.TestMocks = append(impact.TestMocks, entry)
			} else {
				impact.Implementations = append(impact.Implementations, entry)
			}
			pkgs[impl.PkgPath] = struct{}{}
			files[impl.File] = struct{}{}
			if impl.ModuleArea != "" {
				mods[impl.ModuleArea] = struct{}{}
			}
			// Calls through the interface reach concrete methods.
			for _, method := range idx.Symbols {
				if method.PkgPath != impl.PkgPath || method.RecvType != impl.Name || method.Kind != "method" {
					continue
				}
				roots = append(roots, method.ID)
				for _, caller := range idx.Callers[method.ID] {
					callers[caller] = struct{}{}
					if cs := idx.Symbols[caller]; cs != nil {
						pkgs[cs.PkgPath] = struct{}{}
						files[cs.File] = struct{}{}
						if cs.ModuleArea != "" {
							mods[cs.ModuleArea] = struct{}{}
						}
					}
				}
			}
		}
	}

	if sites := idx.CompositeLiterals[sym.ID]; len(sites) > 0 {
		impact.CompositeLiterals = append(impact.CompositeLiterals, sites...)
		for _, site := range sites {
			pkgs[site.FromPkg] = struct{}{}
			files[site.File] = struct{}{}
			if site.FromMod != "" {
				mods[site.FromMod] = struct{}{}
			}
			if site.FromFunc != "" {
				callers[site.FromFunc] = struct{}{}
			}
			if site.Unkeyed {
				impact.UnkeyedLiterals = append(impact.UnkeyedLiterals, site)
			}
		}
	}

	seen := map[string]int{sym.ID: 0}
	queue := append([]string{sym.ID}, roots...)
	for _, root := range roots {
		if _, ok := seen[root]; !ok {
			seen[root] = 0
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] >= depth {
			continue
		}
		for _, caller := range idx.Callers[cur] {
			if _, ok := seen[caller]; ok {
				continue
			}
			seen[caller] = seen[cur] + 1
			queue = append(queue, caller)
			callers[caller] = struct{}{}
			if cs, ok := idx.Symbols[caller]; ok {
				pkgs[cs.PkgPath] = struct{}{}
				files[cs.File] = struct{}{}
				if cs.ModuleArea != "" {
					mods[cs.ModuleArea] = struct{}{}
				}
			}
		}
	}

	result := idx.impact(pkgs, files, mods, callers)
	result.DirectReferences = dedupeRefs(direct)
	result.TransitiveCallers = sortedKeys(callers)
	result.AffectedPackages = sortedPackages(pkgs)
	result.TestPackagesToRun = append([]string{}, result.TestPackages...)
	impact.Implementations = sortImpactSymbols(impact.Implementations)
	impact.TestMocks = sortImpactSymbols(impact.TestMocks)
	if len(impact.Implementations) > 0 || len(impact.TestMocks) > 0 {
		result.Implementations = impact.Implementations
		result.TestMocks = impact.TestMocks
	}
	result.CompositeLiterals = impact.CompositeLiterals
	result.UnkeyedLiterals = impact.UnkeyedLiterals
	return result
}

func dedupeRefs(refs []ImpactRef) []ImpactRef {
	seen := map[ImpactRef]struct{}{}
	out := make([]ImpactRef, 0, len(refs))
	for _, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func sortImpactSymbols(items []ImpactSymbol) []ImpactSymbol {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// ImpactByPackage follows reverse imports.
func (idx *Index) ImpactByPackage(pkgPath string, depth int) Impact {
	pkgs := map[string]struct{}{pkgPath: {}}
	mods := map[string]struct{}{}
	if pi, ok := idx.Packages[pkgPath]; ok && pi.ModuleArea != "" {
		mods[pi.ModuleArea] = struct{}{}
	}

	seen := map[string]int{pkgPath: 0}
	queue := []string{pkgPath}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] >= depth {
			continue
		}
		for _, prev := range idx.RevImports[cur] {
			if _, ok := seen[prev]; ok {
				continue
			}
			seen[prev] = seen[cur] + 1
			queue = append(queue, prev)
			pkgs[prev] = struct{}{}
			if pi, ok := idx.Packages[prev]; ok && pi.ModuleArea != "" {
				mods[pi.ModuleArea] = struct{}{}
			}
		}
	}
	result := idx.impact(pkgs, nil, mods, nil)
	result.AffectedPackages = sortedPackages(pkgs)
	return result
}

func sortedPackages(pkgs map[string]struct{}) []string {
	out := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

func (idx *Index) impact(pkgs, files, mods, callers map[string]struct{}) Impact {
	testPkgs := make([]string, 0)
	for pkg := range pkgs {
		pi, ok := idx.Packages[pkg]
		if !ok {
			// External test package (foo_test): map it to the tested package.
			pi, ok = idx.Packages[strings.TrimSuffix(pkg, "_test")]
			if !ok {
				continue
			}
		}
		if pi.TestFiles == 0 {
			continue
		}
		if pi.RelPath == "." {
			testPkgs = append(testPkgs, ".")
		} else {
			testPkgs = append(testPkgs, "./"+pi.RelPath)
		}
	}
	sort.Strings(testPkgs)

	risk := "low"
	switch {
	case len(mods) >= 4 || len(pkgs) >= 10:
		risk = "high"
	case len(mods) >= 2 || len(pkgs) >= 4:
		risk = "medium"
	}

	return Impact{
		Risk:             risk,
		Modules:          sortedKeys(mods),
		Packages:         len(pkgs),
		Files:            sortedKeys(files),
		Callers:          sortedKeys(callers),
		TestPackages:     testPkgs,
		SuggestedCommand: strings.TrimSpace("go test -race -count=1 " + strings.Join(testPkgs, " ")),
	}
}

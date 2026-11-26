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

// impactAccumulator collects the shared blast-radius sets for symbol and
// package impact analysis.
type impactAccumulator struct {
	pkgs    map[string]struct{}
	files   map[string]struct{}
	mods    map[string]struct{}
	callers map[string]struct{}
	direct  []ImpactRef
}

func newImpactAccumulator(sym *Symbol) *impactAccumulator {
	acc := &impactAccumulator{
		pkgs:    map[string]struct{}{sym.PkgPath: {}},
		files:   map[string]struct{}{sym.File: {}},
		mods:    map[string]struct{}{},
		callers: map[string]struct{}{},
	}
	if sym.ModuleArea != "" {
		acc.mods[sym.ModuleArea] = struct{}{}
	}
	return acc
}

func (acc *impactAccumulator) addRef(ref XRef) {
	acc.pkgs[ref.FromPkg] = struct{}{}
	acc.files[ref.File] = struct{}{}
	if ref.FromMod != "" {
		acc.mods[ref.FromMod] = struct{}{}
	}
	if ref.FromFuncID != "" {
		acc.callers[ref.FromFuncID] = struct{}{}
	}
	acc.direct = append(acc.direct, ImpactRef{
		FromPkg: ref.FromPkg, FromMod: ref.FromMod, FromFuncID: ref.FromFuncID,
		File: ref.File, Line: ref.Line, Test: ref.Test,
	})
}

func (acc *impactAccumulator) addSymbol(sym *Symbol) {
	acc.pkgs[sym.PkgPath] = struct{}{}
	acc.files[sym.File] = struct{}{}
	if sym.ModuleArea != "" {
		acc.mods[sym.ModuleArea] = struct{}{}
	}
}

func (acc *impactAccumulator) addCaller(id string, idx *Index) {
	acc.callers[id] = struct{}{}
	if sym := idx.Symbols[id]; sym != nil {
		acc.addSymbol(sym)
	}
}

func isTypeTarget(sym *Symbol) bool {
	return sym.Kind == "struct" || sym.Kind == "type" || sym.Kind == "interface"
}

func (idx *Index) collectTypeTargetRefs(sym *Symbol, acc *impactAccumulator) {
	for _, other := range idx.Symbols {
		if other.PkgPath != sym.PkgPath || other.RecvType != sym.Name || other.ID == sym.ID {
			continue
		}
		for _, ref := range idx.XRefs[other.ID] {
			acc.addRef(ref)
		}
	}
}

func (idx *Index) collectInterfaceImpact(sym *Symbol, acc *impactAccumulator) (implementations, mocks []ImpactSymbol, roots []string) {
	for _, match := range idx.IfaceImpls[sym.ID] {
		impl := idx.Symbols[match.TypeID]
		if impl == nil {
			continue
		}
		entry := newImpactSymbol(impl)
		if impl.IsTest {
			mocks = append(mocks, entry)
		} else {
			implementations = append(implementations, entry)
		}
		acc.addSymbol(impl)
		// Calls through the interface reach exactly the concrete methods that
		// satisfy it. Promoted methods resolve to their declaring embedded type,
		// and unrelated methods on the implementing type stay out of the blast
		// radius.
		methodIDs := sortedKeys(match.MethodIDs)
		if len(methodIDs) == 0 {
			methodIDs = idx.methodsOfType(impl)
		}
		for _, methodID := range methodIDs {
			roots = append(roots, methodID)
			for _, caller := range idx.Callers[methodID] {
				acc.addCaller(caller, idx)
			}
		}
	}
	return implementations, mocks, roots
}

// methodsOfType is the pre-method-map fallback used by manually constructed
// indexes. Generated indexes always carry ImplMatch.MethodIDs.
func (idx *Index) methodsOfType(impl *Symbol) []string {
	ids := make([]string, 0)
	for _, method := range idx.Symbols {
		if method.PkgPath != impl.PkgPath || method.RecvType != impl.Name || method.Kind != "method" {
			continue
		}
		ids = append(ids, method.ID)
	}
	sort.Strings(ids)
	return ids
}

func (idx *Index) collectCompositeImpact(sym *Symbol, acc *impactAccumulator) (literals, unkeyed []CompositeSite) {
	for _, site := range idx.CompositeLiterals[sym.ID] {
		acc.pkgs[site.FromPkg] = struct{}{}
		acc.files[site.File] = struct{}{}
		if site.FromMod != "" {
			acc.mods[site.FromMod] = struct{}{}
		}
		if site.FromFunc != "" {
			acc.callers[site.FromFunc] = struct{}{}
		}
		literals = append(literals, site)
		if site.Unkeyed {
			unkeyed = append(unkeyed, site)
		}
	}
	return literals, unkeyed
}

func (idx *Index) walkImpactCallers(sym *Symbol, roots []string, depth int, acc *impactAccumulator) {
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
			acc.addCaller(caller, idx)
		}
	}
}

// ImpactBySymbol follows references and transitive callers, expanding
// interface implementations, struct composite literals, fields, and vars.
func (idx *Index) ImpactBySymbol(sym *Symbol, depth int) Impact {
	acc := newImpactAccumulator(sym)
	for _, ref := range idx.XRefs[sym.ID] {
		acc.addRef(ref)
	}

	// Include references to the struct's fields and methods for type targets.
	if isTypeTarget(sym) {
		idx.collectTypeTargetRefs(sym, acc)
	}

	var implementations, mocks []ImpactSymbol
	roots := []string{sym.ID}
	if sym.Kind == "interface" {
		var interfaceRoots []string
		implementations, mocks, interfaceRoots = idx.collectInterfaceImpact(sym, acc)
		roots = append(roots, interfaceRoots...)
	}

	literals, unkeyed := idx.collectCompositeImpact(sym, acc)
	idx.walkImpactCallers(sym, roots, depth, acc)

	result := idx.impact(acc.pkgs, acc.files, acc.mods, acc.callers)
	result.DirectReferences = dedupeRefs(acc.direct)
	result.TransitiveCallers = sortedKeys(acc.callers)
	result.AffectedPackages = sortedPackages(acc.pkgs)
	result.TestPackagesToRun = append([]string{}, result.TestPackages...)
	implementations = sortImpactSymbols(implementations)
	mocks = sortImpactSymbols(mocks)
	if len(implementations) > 0 || len(mocks) > 0 {
		result.Implementations = implementations
		result.TestMocks = mocks
	}
	result.CompositeLiterals = literals
	result.UnkeyedLiterals = unkeyed
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

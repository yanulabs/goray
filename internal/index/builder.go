package index

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"
)

type builder struct {
	idx         *Index
	modPath     string
	modulePaths []string
	absRoot     string

	revImports map[string]map[string]struct{}
	callers    map[string]map[string]struct{}
	callees    map[string]map[string]struct{}
	edgeKinds  map[string]CallEdge
	modOut     map[string]map[string]map[string]struct{}
	modIn      map[string]map[string]map[string]struct{}

	concrete        []concreteType
	ifaces          []ifaceType
	visibleIfaces   []ifaceType
	ifaceOwner      map[types.Object]string
	ifaceMethod     map[string][]string
	fieldOwners     map[*ast.Ident]string
	fieldVarOwner   map[*types.Var]string
	pendingDispatch []dispatchCall
	symbolIDs       map[types.Object]string
	facts           map[string]fileFacts
	testFiles       map[string]map[string]struct{}
	testFilesLoaded bool
}

func newBuilder(idx *Index, absRoot string) *builder {
	return &builder{
		modPath:       idx.ModulePath,
		modulePaths:   append([]string{}, idx.ModulePaths...),
		absRoot:       absRoot,
		revImports:    map[string]map[string]struct{}{},
		callers:       map[string]map[string]struct{}{},
		callees:       map[string]map[string]struct{}{},
		edgeKinds:     map[string]CallEdge{},
		modOut:        map[string]map[string]map[string]struct{}{},
		modIn:         map[string]map[string]map[string]struct{}{},
		ifaceOwner:    map[types.Object]string{},
		ifaceMethod:   map[string][]string{},
		fieldOwners:   map[*ast.Ident]string{},
		fieldVarOwner: map[*types.Var]string{},
		symbolIDs:     map[types.Object]string{},
		facts:         map[string]fileFacts{},
		idx:           idx,
	}
}

// symbolIDFor returns the stable ID assigned to an object. Most objects use
// symbolID directly; repeated init functions carry a deterministic suffix so
// they do not overwrite each other.
func (b *builder) symbolIDFor(obj types.Object) string {
	if id, ok := b.symbolIDs[obj]; ok {
		return id
	}
	return symbolID(obj)
}

func (b *builder) inModule(pkgPath string) bool {
	_, ok := b.owningModule(pkgPath)
	return ok
}

// owningModule returns the longest module path that contains pkgPath.
func (b *builder) owningModule(pkgPath string) (string, bool) {
	best := ""
	for _, mod := range b.modulePaths {
		if pkgPath == mod || strings.HasPrefix(pkgPath, mod+"/") {
			if len(mod) > len(best) {
				best = mod
			}
		}
	}
	if best == "" {
		if pkgPath == b.modPath || strings.HasPrefix(pkgPath, b.modPath+"/") {
			return b.modPath, true
		}
		return "", false
	}
	return best, true
}

func (b *builder) relPackage(pkgPath string) string {
	mod, ok := b.owningModule(pkgPath)
	if !ok {
		mod = b.modPath
	}
	if pkgPath == mod {
		return "."
	}
	return strings.TrimPrefix(pkgPath, mod+"/")
}

// areaFor namespaces module groups by workspace module so two modules cannot
// collide on a shared relative path such as "internal/config".
func (b *builder) areaFor(pkgPath, rel string) string {
	area, _ := moduleArea(rel)
	if len(b.modulePaths) < 2 {
		return area
	}
	mod, ok := b.owningModule(pkgPath)
	if !ok {
		return area
	}
	label := mod
	if i := strings.LastIndex(label, "/"); i >= 0 {
		label = label[i+1:]
	}
	if area == "" || area == "root" {
		return label
	}
	return label + "/" + area
}

func (b *builder) module(name string) *ModSummary {
	if m, ok := b.idx.Modules[name]; ok {
		return m
	}
	m := &ModSummary{Name: name, OutModules: map[string][]string{}, InModules: map[string][]string{}}
	b.idx.Modules[name] = m
	return m
}

func (b *builder) addSymbol(sym *Symbol, pi *PkgInfo) {
	b.idx.Symbols[sym.ID] = sym
	if canonical := sym.CanonicalID(); canonical != "" {
		b.idx.CanonicalSymbols[canonical] = sym
	}
	key := strings.ToLower(sym.Name)
	b.idx.SymbolsByName[key] = append(b.idx.SymbolsByName[key], sym)
	if sym.IsTest {
		return
	}
	if sym.Generated {
		pi.GeneratedSymbols++
		if pi.ModuleArea != "" {
			b.module(pi.ModuleArea).GeneratedSymbols++
		}
	}
	if !sym.Exported {
		return
	}
	pi.Exported++
	if isModuleRoot(pi.RelPath) && sym.ModuleArea != "" {
		b.module(sym.ModuleArea).RootExported++
	}
}

func (b *builder) finish() {
	for k, set := range b.revImports {
		b.idx.RevImports[k] = sortedKeys(set)
	}
	for k, set := range b.callers {
		b.idx.Callers[k] = sortedKeys(set)
	}
	for k, set := range b.callees {
		b.idx.Callees[k] = sortedKeys(set)
	}
	b.idx.CallEdgeKinds = b.edgeKinds
	for src, targets := range b.modOut {
		m := b.module(src)
		for dst, symbols := range targets {
			m.OutModules[dst] = sortedKeys(symbols)
		}
	}
	for dst, sources := range b.modIn {
		m := b.module(dst)
		for src, symbols := range sources {
			m.InModules[src] = sortedKeys(symbols)
		}
	}

	sort.Slice(b.idx.LargeFiles, func(i, j int) bool {
		if b.idx.LargeFiles[i].LOC != b.idx.LargeFiles[j].LOC {
			return b.idx.LargeFiles[i].LOC > b.idx.LargeFiles[j].LOC
		}
		return b.idx.LargeFiles[i].Path < b.idx.LargeFiles[j].Path
	})
	for _, m := range b.idx.Modules {
		m.AllUntracked = m.Files > 0 && m.UntrackedFiles == m.Files
		m.TestOnly = m.ProdLOC == 0 && m.TestLOC > 0
		sort.Slice(m.LargeFiles, func(i, j int) bool {
			if m.LargeFiles[i].LOC != m.LargeFiles[j].LOC {
				return m.LargeFiles[i].LOC > m.LargeFiles[j].LOC
			}
			return m.LargeFiles[i].Path < m.LargeFiles[j].Path
		})
	}
}

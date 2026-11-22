package index

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

const largeFileLOC = 400

// BuildOptions controls package loading.
type BuildOptions struct {
	// Tags is the normalized set of Go build tags passed to the loader.
	Tags []string
	// Workspace enables multi-module go.work indexing. When false, a root
	// directory that contains go.work but no go.mod is auto-detected.
	Workspace bool
}

// NormalizeTags splits comma- or space-separated tags, trims, deduplicates,
// and sorts them so equivalent tag sets produce identical cache keys.
func NormalizeTags(raw []string) []string {
	seen := map[string]struct{}{}
	for _, value := range raw {
		for _, tag := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			seen[tag] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// Build loads the module rooted at rootDir and computes a complete index.
func Build(rootDir string) (*Index, error) {
	return BuildWithOptions(rootDir, BuildOptions{})
}

// BuildWithOptions loads a module (or go.work workspace) and computes a
// complete index with the requested build tags.
func BuildWithOptions(rootDir string, opts BuildOptions) (*Index, error) {
	start := time.Now()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	opts.Tags = NormalizeTags(opts.Tags)
	if !opts.Workspace {
		opts.Workspace = workspaceRootAt(absRoot)
	}

	pkgs, err := loadPackages(absRoot, opts)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", absRoot)
	}

	testPkgs := loadTestPackages(absRoot, opts)
	return buildFromPackages(absRoot, opts, pkgs, testPkgs, start)
}

// buildFromPackages computes all derived structures from type-checked package
// artifacts. Incremental rebuilds merge preloaded unaffected packages with
// freshly loaded affected packages and reuse this exact path.
func buildFromPackages(absRoot string, opts BuildOptions, pkgs, testPkgs []*packages.Package, start time.Time) (*Index, error) {
	if loadErr := packageLoadErrors(pkgs); loadErr != nil {
		return nil, loadErr
	}
	modulePaths := findModulePaths(pkgs)
	if len(modulePaths) == 0 {
		return nil, fmt.Errorf("cannot determine module path for %s", absRoot)
	}
	modPath := modulePaths[0]

	untracked := detectUntracked(absRoot)
	idx := newIndex(absRoot, modPath, modulePaths, untracked)
	idx.BuildTags = opts.Tags
	idx.Workspace = opts.Workspace

	b := newBuilder(idx, absRoot)
	b.indexPackages(pkgs)
	b.indexTestDefinitions(testPkgs)
	b.indexReferences(pkgs)
	b.indexTestReferences(testPkgs)
	b.collectImportSites(pkgs)
	b.testPass = true
	b.collectImportSites(testPkgs)
	b.testPass = false
	b.collectImportedInterfaces(pkgs)
	b.matchInterfaces()
	b.applyDispatchEdges()
	b.finish()
	b.computeTestCoverage()
	b.buildSummary(pkgs)
	if hasTestFiles(b.idx.Packages) {
		b.idx.TestLoadErrors = collectTestLoadErrors(testPkgs)
	}

	idx.pkgs = pkgs
	idx.testPkgs = testPkgs
	idx.BuiltAt = time.Now()
	idx.BuildDuration = time.Since(start)
	return idx, nil
}

func newIndex(absRoot, modPath string, modulePaths []string, untracked map[string]bool) *Index {
	return &Index{
		RootDir:           absRoot,
		ModulePath:        modPath,
		ModulePaths:       modulePaths,
		Packages:          map[string]*PkgInfo{},
		RevImports:        map[string][]string{},
		Symbols:           map[string]*Symbol{},
		SymbolsByName:     map[string][]*Symbol{},
		CanonicalSymbols:  map[string]*Symbol{},
		XRefs:             map[string][]XRef{},
		Callers:           map[string][]string{},
		Callees:           map[string][]string{},
		CallEdgeKinds:     map[string]CallEdge{},
		CompositeLiterals: map[string][]CompositeSite{},
		IfaceImpls:        map[string][]ImplMatch{},
		TypeImpls:         map[string][]ImplMatch{},
		IfaceMethods:      map[string]bool{},
		Modules:           map[string]*ModSummary{},
		TestCovered:       map[string]bool{},
		UntrackedFiles:    untracked,
	}
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
		idx:           idx,
	}
}

var loadMode = packages.NeedName | packages.NeedFiles |
	packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
	packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule

func loadPackages(absRoot string, opts BuildOptions) ([]*packages.Package, error) {
	config := &packages.Config{Dir: absRoot, Mode: loadMode}
	if len(opts.Tags) > 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.Tags, ",")}
	}

	patterns := []string{"./..."}
	if opts.Workspace {
		dirs, err := workspaceModules(absRoot)
		if err != nil {
			return nil, err
		}
		patterns = dirs
	}

	seen := map[string]bool{}
	var all []*packages.Package
	for _, pattern := range patterns {
		cfg := *config
		if opts.Workspace {
			cfg.Dir = pattern
			pattern = "./..."
		}
		loaded, err := packages.Load(&cfg, pattern)
		if err != nil {
			return nil, fmt.Errorf("load packages: %w", err)
		}
		for _, p := range loaded {
			if seen[p.PkgPath] {
				continue
			}
			seen[p.PkgPath] = true
			all = append(all, p)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].PkgPath < all[j].PkgPath })
	return all, nil
}

// loadTestPackages type-checks test variants with the same build tags so test
// mocks, test-only methods, and test call edges are visible to impact,
// implementation, and coverage analysis.
func loadTestPackages(absRoot string, opts BuildOptions) []*packages.Package {
	config := &packages.Config{
		Dir:   absRoot,
		Tests: true,
		Mode:  loadMode | packages.NeedForTest,
	}
	if len(opts.Tags) > 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.Tags, ",")}
	}
	dirs := []string{absRoot}
	if opts.Workspace {
		modules, err := workspaceModules(absRoot)
		if err != nil {
			return nil
		}
		dirs = modules
	}
	seen := map[string]bool{}
	var out []*packages.Package
	for _, dir := range dirs {
		cfg := *config
		cfg.Dir = dir
		pkgs, err := packages.Load(&cfg, "./...")
		if err != nil {
			continue
		}
		for _, p := range pkgs {
			if p.ForTest == "" || seen[p.ID] {
				continue
			}
			if p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test") {
				continue
			}
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// workspaceModules parses the active go.work and returns the absolute module
// directories. Relative use paths are resolved against the workspace root.
func workspaceModules(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	work, err := modfile.ParseWork(filepath.Join(root, "go.work"), data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}
	if len(work.Use) == 0 {
		return nil, fmt.Errorf("go.work in %s has no use directives", root)
	}
	dirs := make([]string, 0, len(work.Use))
	for _, use := range work.Use {
		dir := use.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	sort.Strings(dirs)
	return dirs, nil
}

// LoadError reports package parse/type errors that must not replace a healthy snapshot.
type LoadError struct {
	Errors []string
}

func (e *LoadError) Error() string {
	return "package load errors:\n" + strings.Join(e.Errors, "\n")
}

func packageLoadErrors(pkgs []*packages.Package) error {
	var messages []string
	for _, p := range pkgs {
		for _, loadErr := range p.Errors {
			messages = append(messages, p.PkgPath+": "+loadErr.Error())
		}
		if len(p.GoFiles) == 0 {
			continue
		}
		if p.Types == nil || p.TypesInfo == nil || p.Fset == nil || len(p.Syntax) == 0 {
			messages = append(messages, p.PkgPath+": incomplete syntax or type information")
			continue
		}
		if p.IllTyped || !p.Types.Complete() {
			messages = append(messages, p.PkgPath+": type checking incomplete")
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &LoadError{Errors: messages}
}

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

	// testPass limits definition and reference indexing to *_test.go files.
	testPass bool
}

func hasTestFiles(packagesByPath map[string]*PkgInfo) bool {
	for _, pkg := range packagesByPath {
		if pkg.TestFiles > 0 {
			return true
		}
	}
	return false
}

func collectTestLoadErrors(pkgs []*packages.Package) []string {
	seen := map[string]struct{}{}
	for _, p := range pkgs {
		if p.ForTest == "" {
			continue
		}
		for _, loadErr := range p.Errors {
			seen[p.PkgPath+": "+loadErr.Error()] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for message := range seen {
		out = append(out, message)
	}
	sort.Strings(out)
	return out
}

// findModulePaths returns every distinct module path in the loaded package
// set. Workspace builds have more than one; single-module builds have one.
func findModulePaths(pkgs []*packages.Package) []string {
	seen := map[string]struct{}{}
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path != "" {
			seen[p.Module.Path] = struct{}{}
		}
	}
	if len(seen) == 0 && len(pkgs) > 0 {
		if path := modulePathFromList(pkgs[0]); path != "" {
			seen[path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func modulePathFromList(p *packages.Package) string {
	dir := ""
	for _, file := range p.GoFiles {
		dir = filepath.Dir(file)
		break
	}
	if dir == "" {
		return ""
	}
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if fields := strings.Fields(string(out)); len(fields) > 0 {
		return fields[0]
	}
	return ""
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

func (b *builder) indexPackages(pkgs []*packages.Package) {
	for _, p := range pkgs {
		for _, e := range p.Errors {
			b.idx.LoadErrors = append(b.idx.LoadErrors, e.Error())
		}
		if !b.inModule(p.PkgPath) {
			continue
		}

		rel := b.relPackage(p.PkgPath)
		area := b.areaFor(p.PkgPath, rel)
		pi := &PkgInfo{
			PkgPath:    p.PkgPath,
			RelPath:    rel,
			Name:       p.Name,
			ModuleArea: area,
			ProdFiles:  len(p.GoFiles),
		}
		if len(p.GoFiles) > 0 {
			pi.Dir = filepath.Dir(p.GoFiles[0])
		} else if p.Module != nil && p.Module.Dir != "" {
			// Test-only packages have no production files; derive the directory
			// from the owning module so *_test.go metrics are still collected.
			pi.Dir = filepath.Join(p.Module.Dir, filepath.FromSlash(rel))
		}

		for impPath := range p.Imports {
			if !b.inModule(impPath) {
				continue
			}
			pi.Imports = append(pi.Imports, impPath)
			if b.revImports[impPath] == nil {
				b.revImports[impPath] = map[string]struct{}{}
			}
			b.revImports[impPath][p.PkgPath] = struct{}{}
		}
		sort.Strings(pi.Imports)

		b.collectFileMetrics(p, pi)
		pi.TestOnly = pi.ProdLOC == 0 && pi.TestLOC > 0

		b.idx.Packages[p.PkgPath] = pi
		if area != "" {
			m := b.module(area)
			m.Packages++
			m.TestLOC += pi.TestLOC
		}

		b.indexDefinitions(p, pi)
	}
}

// pkgInfoFor resolves the production package record that owns a test variant.
func (b *builder) pkgInfoFor(p *packages.Package) (*PkgInfo, bool) {
	if pi, ok := b.idx.Packages[p.PkgPath]; ok {
		return pi, true
	}
	if p.ForTest != "" {
		if pi, ok := b.idx.Packages[p.ForTest]; ok {
			return pi, true
		}
	}
	path := strings.TrimSuffix(p.PkgPath, "_test")
	if pi, ok := b.idx.Packages[path]; ok {
		return pi, true
	}
	return nil, false
}

func (b *builder) indexTestDefinitions(pkgs []*packages.Package) {
	b.testPass = true
	defer func() { b.testPass = false }()
	for _, p := range pkgs {
		pi, ok := b.pkgInfoFor(p)
		if !ok {
			continue
		}
		b.indexDefinitions(p, pi)
	}
}

func (b *builder) indexTestReferences(pkgs []*packages.Package) {
	b.testPass = true
	defer func() { b.testPass = false }()
	b.indexReferences(pkgs)
}

func (b *builder) collectFileMetrics(p *packages.Package, pi *PkgInfo) {
	var mod *ModSummary
	if pi.ModuleArea != "" {
		mod = b.module(pi.ModuleArea)
	}
	for _, path := range p.GoFiles {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel := relFile(b.absRoot, path)
		generated := isGenerated(filepath.Base(path), src)
		untracked := b.idx.UntrackedFiles[rel]
		pi.Files++
		if mod != nil {
			mod.Files++
		}
		if untracked {
			pi.UntrackedFiles++
			if mod != nil {
				mod.UntrackedFiles++
			}
		}
		loc := countLines(src)
		if generated {
			pi.GeneratedFiles++
			pi.GeneratedLOC += loc
			if mod != nil {
				mod.GeneratedFiles++
				mod.GeneratedLOC += loc
			}
			continue
		}
		pi.ProdLOC += loc
		if mod != nil {
			mod.ProdLOC += loc
		}
		if loc < largeFileLOC {
			continue
		}
		metric := FileMetric{Path: rel, LOC: loc, Generated: generated, Untracked: untracked}
		b.idx.LargeFiles = append(b.idx.LargeFiles, metric)
		if mod != nil {
			mod.LargeFiles = append(mod.LargeFiles, metric)
		}
	}

	if pi.Dir == "" {
		return
	}
	entries, err := os.ReadDir(pi.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		pi.TestFiles++
		if src, err := os.ReadFile(filepath.Join(pi.Dir, entry.Name())); err == nil {
			pi.TestLOC += countLines(src)
		}
	}
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

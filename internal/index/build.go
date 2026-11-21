package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

const largeFileLOC = 400

// Build loads the module rooted at rootDir and computes a complete index.
func Build(rootDir string) (*Index, error) {
	start := time.Now()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	pkgs, err := packages.Load(&packages.Config{
		Dir: absRoot,
		Mode: packages.NeedName | packages.NeedFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
	}, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", absRoot)
	}
	if loadErr := packageLoadErrors(pkgs); loadErr != nil {
		return nil, loadErr
	}

	modPath := findModulePath(absRoot, pkgs)
	if modPath == "" {
		return nil, fmt.Errorf("cannot determine module path for %s", absRoot)
	}

	untracked := detectUntracked(absRoot)

	b := &builder{
		modPath:    modPath,
		absRoot:    absRoot,
		revImports: map[string]map[string]struct{}{},
		callers:    map[string]map[string]struct{}{},
		callees:    map[string]map[string]struct{}{},
		modOut:     map[string]map[string]map[string]struct{}{},
		modIn:      map[string]map[string]map[string]struct{}{},
		idx: &Index{
			RootDir:          absRoot,
			ModulePath:       modPath,
			Packages:         map[string]*PkgInfo{},
			RevImports:       map[string][]string{},
			Symbols:          map[string]*Symbol{},
			SymbolsByName:    map[string][]*Symbol{},
			CanonicalSymbols: map[string]*Symbol{},
			XRefs:            map[string][]XRef{},
			Callers:          map[string][]string{},
			Callees:          map[string][]string{},
			IfaceImpls:       map[string][]ImplMatch{},
			TypeImpls:        map[string][]ImplMatch{},
			IfaceMethods:     map[string]bool{},
			Modules:          map[string]*ModSummary{},
			TestCovered:      map[string]bool{},
			UntrackedFiles:   untracked,
		},
	}

	b.indexPackages(pkgs)
	b.indexReferences(pkgs)
	b.collectImportedInterfaces(pkgs)
	b.matchInterfaces()
	b.finish()
	b.computeTestCoverage()
	b.buildSummary(pkgs)
	if hasTestFiles(b.idx.Packages) {
		b.idx.TestLoadErrors = collectTestLoadErrors(absRoot)
	}

	b.idx.BuiltAt = time.Now()
	b.idx.BuildDuration = time.Since(start)
	return b.idx, nil
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
	idx     *Index
	modPath string
	absRoot string

	revImports map[string]map[string]struct{}
	callers    map[string]map[string]struct{}
	callees    map[string]map[string]struct{}
	modOut     map[string]map[string]map[string]struct{}
	modIn      map[string]map[string]map[string]struct{}

	concrete      []concreteType
	ifaces        []ifaceType
	visibleIfaces []ifaceType
}

func hasTestFiles(packagesByPath map[string]*PkgInfo) bool {
	for _, pkg := range packagesByPath {
		if pkg.TestFiles > 0 {
			return true
		}
	}
	return false
}

func collectTestLoadErrors(dir string) []string {
	pkgs, err := packages.Load(&packages.Config{
		Dir:   dir,
		Tests: true,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo | packages.NeedModule | packages.NeedForTest,
	}, "./...")
	if err != nil {
		return []string{err.Error()}
	}
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

func findModulePath(dir string, pkgs []*packages.Package) string {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path != "" {
			return p.Module.Path
		}
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
	return pkgPath == b.modPath || strings.HasPrefix(pkgPath, b.modPath+"/")
}

func (b *builder) relPackage(pkgPath string) string {
	if pkgPath == b.modPath {
		return "."
	}
	return strings.TrimPrefix(pkgPath, b.modPath+"/")
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
		area, _ := moduleArea(rel)
		pi := &PkgInfo{
			PkgPath:    p.PkgPath,
			RelPath:    rel,
			Name:       p.Name,
			ModuleArea: area,
			ProdFiles:  len(p.GoFiles),
		}
		if len(p.GoFiles) > 0 {
			pi.Dir = filepath.Dir(p.GoFiles[0])
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

		b.idx.Packages[p.PkgPath] = pi
		if area != "" {
			m := b.module(area)
			m.Packages++
			m.TestLOC += pi.TestLOC
		}

		b.indexDefinitions(p, pi)
	}
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
		if generated {
			pi.GeneratedFiles++
			if mod != nil {
				mod.GeneratedFiles++
			}
			continue
		}
		loc := countLines(src)
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
		sort.Slice(m.LargeFiles, func(i, j int) bool {
			if m.LargeFiles[i].LOC != m.LargeFiles[j].LOC {
				return m.LargeFiles[i].LOC > m.LargeFiles[j].LOC
			}
			return m.LargeFiles[i].Path < m.LargeFiles[j].Path
		})
	}
}

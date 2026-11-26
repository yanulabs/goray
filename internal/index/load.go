package index

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// loadMode requests typed syntax only for the initial packages. NeedDeps must
// stay out: it propagates NeedSyntax and NeedTypesInfo to every transitive
// dependency, which parses and retains the entire dependency source graph.
var loadMode = packages.NeedName | packages.NeedFiles |
	packages.NeedImports | packages.NeedTypes |
	packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule

// loadPackages type-checks production packages and their test variants in one
// loader pass. Separate loads duplicate export-data imports and type checking,
// which doubles peak memory and startup time on repositories with broad test
// coverage.
func loadPackages(ctx context.Context, absRoot string, opts BuildOptions) ([]*packages.Package, []*packages.Package, error) {
	config := &packages.Config{
		Context: ctx,
		Dir:     absRoot,
		Tests:   true,
		Mode:    loadMode | packages.NeedForTest,
	}
	if len(opts.Tags) > 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.Tags, ",")}
	}

	patterns := []string{"./..."}
	if opts.Workspace {
		dirs, err := workspaceModules(absRoot)
		if err != nil {
			return nil, nil, err
		}
		patterns = workspacePatterns(absRoot, dirs)
	}

	// One loader call from the workspace root keeps a single go/types universe
	// across modules. Loading each module in its own call produces disjoint
	// type objects from export data and breaks field ownership, interface
	// satisfaction, and method-set lookups that cross module boundaries.
	loaded := newPackageSet()
	pkgs, err := packages.Load(config, patterns...)
	if err != nil {
		return nil, nil, fmt.Errorf("load packages: %w", err)
	}
	loaded.add(pkgs)
	production, tests := loaded.sorted()
	return production, tests, nil
}

// workspacePatterns converts absolute module directories into single-load
// patterns relative to the workspace root, including modules outside it.
func workspacePatterns(root string, dirs []string) []string {
	patterns := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			patterns = append(patterns, filepath.ToSlash(dir)+"/...")
			continue
		}
		if rel == "." {
			patterns = append(patterns, "./...")
			continue
		}
		patterns = append(patterns, "./"+filepath.ToSlash(rel)+"/...")
	}
	return patterns
}

type packageSet struct {
	production []*packages.Package
	tests      []*packages.Package
	seen       map[string]bool
	testSeen   map[string]bool
}

func newPackageSet() *packageSet {
	return &packageSet{
		seen:     map[string]bool{},
		testSeen: map[string]bool{},
	}
}

func (s *packageSet) add(pkgs []*packages.Package) {
	for _, p := range pkgs {
		if p == nil || isTestBinary(p) {
			continue
		}
		if p.ForTest == "" {
			if s.seen[p.PkgPath] {
				continue
			}
			s.seen[p.PkgPath] = true
			s.production = append(s.production, p)
			continue
		}
		if s.testSeen[p.ID] {
			continue
		}
		s.testSeen[p.ID] = true
		s.tests = append(s.tests, p)
	}
}

func (s *packageSet) sorted() ([]*packages.Package, []*packages.Package) {
	sort.Slice(s.production, func(i, j int) bool { return s.production[i].PkgPath < s.production[j].PkgPath })
	sort.Slice(s.tests, func(i, j int) bool { return s.tests[i].ID < s.tests[j].ID })
	return s.production, s.tests
}

func isTestBinary(p *packages.Package) bool {
	return p != nil && p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test")
}

// WorkspaceModuleDirs parses the go.work at root and returns its module
// directories. It is used by the watcher to follow modules outside the
// workspace root.
func WorkspaceModuleDirs(root string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return workspaceModules(absRoot)
}

// workspaceModules parses the active go.work and returns the absolute module
// directories. Relative use paths are resolved against the workspace file's
// directory, which honors GOWORK files with custom names.
func workspaceModules(root string) ([]string, error) {
	workFile, ok := ActiveGoWorkFile(root)
	if !ok {
		return nil, fmt.Errorf("read go.work: no workspace file found for %s", root)
	}
	data, err := os.ReadFile(workFile)
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	work, err := modfile.ParseWork(workFile, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}
	if len(work.Use) == 0 {
		return nil, fmt.Errorf("go.work in %s has no use directives", filepath.Dir(workFile))
	}
	baseDir := filepath.Dir(workFile)
	dirs := make([]string, 0, len(work.Use))
	for _, use := range work.Use {
		dir := use.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	sort.Strings(dirs)
	return dirs, nil
}

// LoadError reports package parse/type errors that must not replace a healthy
// snapshot. Errors keeps the historical flat string form used by project_info;
// Diagnostics carries the same failures in structured form for get_diagnostics.
type LoadError struct {
	Errors      []string
	Diagnostics []Diagnostic
}

func (e *LoadError) Error() string {
	return "package load errors:\n" + strings.Join(e.Errors, "\n")
}

func packageLoadErrors(root string, pkgs []*packages.Package) error {
	var messages []string
	var diagnostics []Diagnostic
	for _, p := range pkgs {
		for _, loadErr := range p.Errors {
			messages = append(messages, p.PkgPath+": "+loadErr.Error())
			diagnostics = append(diagnostics, newDiagnostic(root, p.PkgPath, false, loadErr))
		}
		if len(p.GoFiles) == 0 {
			continue
		}
		if p.Types == nil || p.TypesInfo == nil || p.Fset == nil || len(p.Syntax) == 0 {
			message := "incomplete syntax or type information"
			messages = append(messages, p.PkgPath+": "+message)
			diagnostics = append(diagnostics, Diagnostic{
				Package: p.PkgPath, Kind: DiagnosticKindType, Message: message,
			})
			continue
		}
		if p.IllTyped || !p.Types.Complete() {
			message := "type checking incomplete"
			messages = append(messages, p.PkgPath+": "+message)
			diagnostics = append(diagnostics, Diagnostic{
				Package: p.PkgPath, Kind: DiagnosticKindType, Message: message,
			})
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &LoadError{Errors: messages, Diagnostics: uniqueDiagnostics(diagnostics)}
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

// collectTestDiagnostics mirrors collectTestLoadErrors for the structured tool
// payload. Test variants may include production files, so only packages that
// actually belong to a test variant contribute entries.
func collectTestDiagnostics(root string, pkgs []*packages.Package) []Diagnostic {
	var out []Diagnostic
	seen := map[Diagnostic]struct{}{}
	for _, p := range pkgs {
		if p.ForTest == "" {
			continue
		}
		for _, loadErr := range p.Errors {
			diagnostic := newDiagnostic(root, p.PkgPath, true, loadErr)
			if _, ok := seen[diagnostic]; ok {
				continue
			}
			seen[diagnostic] = struct{}{}
			out = append(out, diagnostic)
		}
	}
	sortDiagnostics(out)
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

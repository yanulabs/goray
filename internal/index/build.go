package index

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/tools/go/packages"
)

// Build loads the module rooted at rootDir and computes a complete index.
func Build(rootDir string) (*Index, error) {
	return BuildContext(context.Background(), rootDir)
}

// BuildContext is Build with a cancellation context for package loading.
func BuildContext(ctx context.Context, rootDir string) (*Index, error) {
	return BuildWithOptionsContext(ctx, rootDir, BuildOptions{})
}

// BuildWithOptions loads a module (or go.work workspace) and computes a
// complete index with the requested build tags.
func BuildWithOptions(rootDir string, opts BuildOptions) (*Index, error) {
	return BuildWithOptionsContext(context.Background(), rootDir, opts)
}

// BuildWithOptionsContext is BuildWithOptions with cancellation.
func BuildWithOptionsContext(ctx context.Context, rootDir string, opts BuildOptions) (*Index, error) {
	start := time.Now()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	opts.Tags = NormalizeTags(opts.Tags)
	if !opts.Workspace {
		opts.Workspace = workspaceRootAt(absRoot)
	}

	pkgs, testPkgs, err := loadPackages(ctx, absRoot, opts)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", absRoot)
	}

	return buildFromPackages(ctx, absRoot, opts, pkgs, testPkgs, start)
}

// buildFromPackages computes all derived structures from type-checked package
// artifacts. Incremental rebuilds merge preloaded unaffected packages with
// freshly loaded affected packages and reuse this exact path.
func buildFromPackages(ctx context.Context, absRoot string, opts BuildOptions, pkgs, testPkgs []*packages.Package, start time.Time) (*Index, error) {
	if loadErr := packageLoadErrors(absRoot, pkgs); loadErr != nil {
		return nil, loadErr
	}
	modulePaths := findModulePaths(pkgs)
	if len(modulePaths) == 0 {
		return nil, fmt.Errorf("cannot determine module path for %s", absRoot)
	}
	modPath := modulePaths[0]

	untracked := detectUntrackedContext(ctx, absRoot)
	if err := ctx.Err(); err != nil {
		// Untracked detection is best effort, so a canceled context can yield
		// an empty set. Never publish a snapshot with incomplete provenance.
		return nil, err
	}
	idx := newIndex(absRoot, modPath, modulePaths, untracked)
	idx.BuildTags = opts.Tags
	idx.Workspace = opts.Workspace

	b := newBuilder(idx, absRoot)
	b.registerTestPackages(testPkgs)
	b.indexPackages(pkgs)
	b.indexTestDefinitions(testPkgs)
	b.indexReferences(pkgs, false)
	b.indexReferences(testPkgs, true)
	b.collectImportSites(pkgs, false)
	b.collectImportSites(testPkgs, true)
	b.collectImportedInterfaces(pkgs)
	b.matchInterfaces()
	b.applyDispatchEdges()
	b.finish()
	b.computeTestCoverage()
	b.buildSummary(pkgs, testPkgs)
	b.idx.Diagnostics = collectTestDiagnostics(absRoot, testPkgs)
	if hasTestFiles(b.idx.Packages) {
		b.idx.TestLoadErrors = collectTestLoadErrors(testPkgs)
	}

	if opts.ReleaseArtifacts {
		idx.pkgs = nil
		idx.testPkgs = nil
	} else {
		idx.pkgs = pkgs
		idx.testPkgs = testPkgs
	}
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

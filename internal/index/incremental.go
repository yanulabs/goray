package index

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

// BuildIncremental reloads only packages affected by changed files. The second
// return value reports whether an incremental path was used. Any unsafe or
// ambiguous condition falls back to a full rebuild.
func BuildIncremental(rootDir string, opts BuildOptions, prev *Index, changed []string) (*Index, bool, error) {
	return BuildIncrementalContext(context.Background(), rootDir, opts, prev, changed)
}

// BuildIncrementalContext is BuildIncremental with cancellation.
func BuildIncrementalContext(ctx context.Context, rootDir string, opts BuildOptions, prev *Index, changed []string) (*Index, bool, error) {
	start := time.Now()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, false, err
	}
	opts.Tags = NormalizeTags(opts.Tags)
	if !opts.Workspace {
		opts.Workspace = workspaceRootAt(absRoot)
	}

	full := func() (*Index, bool, error) {
		idx, err := BuildWithOptionsContext(ctx, absRoot, opts)
		return idx, false, err
	}
	if prev == nil || prev.RootDir != absRoot || len(prev.pkgs) == 0 {
		return full()
	}
	if prev.Workspace != opts.Workspace || !sameStrings(prev.BuildTags, opts.Tags) || len(changed) == 0 {
		return full()
	}

	dirty := map[string]bool{}
	for _, path := range changed {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(absRoot, filepath.FromSlash(path))
		}
		abs = filepath.Clean(abs)
		base := filepath.Base(abs)
		if base == "go.mod" || base == "go.sum" || base == "go.work" {
			return full()
		}
		pkgPath, ok := packageForFile(prev, abs)
		if !ok {
			// A file outside any previously known package usually means a new
			// package or directory; a blind incremental merge would miss it.
			return full()
		}
		dirty[pkgPath] = true
	}
	if len(dirty) == 0 {
		return full()
	}
	for _, info := range prev.Packages {
		if info.Dir == "" {
			continue
		}
		if stat, err := os.Stat(info.Dir); err != nil || !stat.IsDir() {
			return full()
		}
	}

	affected := reverseImportClosure(prev, dirty)
	if len(affected)*2 > len(prev.Packages) {
		return full()
	}

	pkgs, testPkgs, ok := loadAffectedPackages(ctx, opts, prev, affected)
	if !ok {
		return full()
	}
	if loadErr := packageLoadErrors(pkgs); loadErr != nil {
		// Snapshot safety: surface the error and keep the previous healthy
		// snapshot at the Store level.
		return nil, true, loadErr
	}

	merged := mergePackages(prev.pkgs, pkgs)
	mergedTests := mergeTestPackages(prev.testPkgs, testPkgs, affected)
	idx, err := buildFromPackages(ctx, absRoot, opts, merged, mergedTests, start)
	if err != nil {
		return nil, true, err
	}
	return idx, true, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// packageForFile maps an absolute file path to its exact owning package. A
// directory that is not an existing package directory is not guessed.
func packageForFile(idx *Index, absPath string) (string, bool) {
	dir := filepath.Dir(absPath)
	for pkgPath, info := range idx.Packages {
		if info.Dir == dir {
			return pkgPath, true
		}
	}
	return "", false
}

func reverseImportClosure(idx *Index, dirty map[string]bool) map[string]bool {
	affected := map[string]bool{}
	queue := make([]string, 0, len(dirty))
	for pkg := range dirty {
		affected[pkg] = true
		queue = append(queue, pkg)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, importer := range idx.RevImports[cur] {
			if affected[importer] {
				continue
			}
			affected[importer] = true
			queue = append(queue, importer)
		}
	}
	return affected
}

// incrementalLoadHook lets tests observe exactly which packages are reloaded.
var incrementalLoadHook func([]string)

func loadAffectedPackages(ctx context.Context, opts BuildOptions, prev *Index, affected map[string]bool) ([]*packages.Package, []*packages.Package, bool) {
	paths := make([]string, 0, len(affected))
	for pkg := range affected {
		paths = append(paths, pkg)
	}
	sort.Strings(paths)
	if incrementalLoadHook != nil {
		incrementalLoadHook(append([]string{}, paths...))
	}

	config := &packages.Config{Context: ctx, Dir: prev.RootDir, Mode: loadMode}
	if len(opts.Tags) > 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.Tags, ",")}
	}
	// One load for every affected package keeps go list and export-data work
	// proportional to the dependency graph instead of the package count.
	loaded, err := packages.Load(config, paths...)
	if err != nil {
		return nil, nil, false
	}
	byPath := make(map[string]*packages.Package, len(loaded))
	for _, pkg := range loaded {
		if pkg.PkgPath != "" {
			byPath[pkg.PkgPath] = pkg
		}
	}
	pkgs := make([]*packages.Package, 0, len(paths))
	for _, pkgPath := range paths {
		info := prev.Packages[pkgPath]
		if info == nil || info.Dir == "" {
			return nil, nil, false
		}
		found := byPath[pkgPath]
		if found == nil {
			return nil, nil, false
		}
		pkgs = append(pkgs, found)
	}

	testConfig := *config
	testConfig.Mode |= packages.NeedForTest
	testConfig.Tests = true
	testLoaded, err := packages.Load(&testConfig, paths...)
	if err != nil {
		return nil, nil, false
	}
	testSeen := map[string]*packages.Package{}
	testPkgs := make([]*packages.Package, 0, len(testLoaded))
	for _, pkg := range testLoaded {
		if pkg.ForTest == "" || pkg.Name == "main" && strings.HasSuffix(pkg.PkgPath, ".test") {
			continue
		}
		if testSeen[pkg.ID] != nil {
			continue
		}
		testSeen[pkg.ID] = pkg
		testPkgs = append(testPkgs, pkg)
	}
	return pkgs, testPkgs, true
}

func mergePackages(prev, fresh []*packages.Package) []*packages.Package {
	merged := make(map[string]*packages.Package, len(prev))
	for _, p := range prev {
		merged[p.PkgPath] = p
	}
	for _, p := range fresh {
		merged[p.PkgPath] = p
	}
	out := make([]*packages.Package, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PkgPath < out[j].PkgPath })
	return out
}

func mergeTestPackages(prev, fresh []*packages.Package, affected map[string]bool) []*packages.Package {
	merged := make(map[string]*packages.Package, len(prev))
	for _, p := range prev {
		if affected[p.ForTest] || affected[strings.TrimSuffix(p.PkgPath, "_test")] {
			continue
		}
		merged[p.ID] = p
	}
	for _, p := range fresh {
		merged[p.ID] = p
	}
	out := make([]*packages.Package, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

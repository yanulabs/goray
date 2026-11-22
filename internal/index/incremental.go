package index

import (
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
		idx, err := BuildWithOptions(absRoot, opts)
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

	pkgs, testPkgs, ok := loadAffectedPackages(opts, prev, affected)
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
	idx, err := buildFromPackages(absRoot, opts, merged, mergedTests, start)
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

func loadAffectedPackages(opts BuildOptions, prev *Index, affected map[string]bool) ([]*packages.Package, []*packages.Package, bool) {
	paths := make([]string, 0, len(affected))
	for pkg := range affected {
		paths = append(paths, pkg)
	}
	sort.Strings(paths)
	if incrementalLoadHook != nil {
		incrementalLoadHook(append([]string{}, paths...))
	}

	config := &packages.Config{Mode: loadMode}
	if len(opts.Tags) > 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.Tags, ",")}
	}
	seen := map[string]*packages.Package{}
	var loaded []*packages.Package
	for _, pkgPath := range paths {
		info := prev.Packages[pkgPath]
		if info == nil || info.Dir == "" {
			return nil, nil, false
		}
		cfg := *config
		cfg.Dir = info.Dir
		pkgs, err := packages.Load(&cfg, ".")
		if err != nil {
			return nil, nil, false
		}
		var found *packages.Package
		for _, p := range pkgs {
			if p.PkgPath == pkgPath {
				found = p
				break
			}
		}
		if found == nil {
			return nil, nil, false
		}
		seen[pkgPath] = found
		loaded = append(loaded, found)
	}

	testSeen := map[string]*packages.Package{}
	testConfig := *config
	testConfig.Mode |= packages.NeedForTest
	testConfig.Tests = true
	var testLoaded []*packages.Package
	for _, pkgPath := range paths {
		info := prev.Packages[pkgPath]
		cfg := testConfig
		cfg.Dir = info.Dir
		pkgs, err := packages.Load(&cfg, ".")
		if err != nil {
			return nil, nil, false
		}
		for _, p := range pkgs {
			if p.ForTest == "" || p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test") {
				continue
			}
			if testSeen[p.ID] != nil {
				continue
			}
			testSeen[p.ID] = p
			testLoaded = append(testLoaded, p)
		}
	}
	return loaded, testLoaded, true
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

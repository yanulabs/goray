package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const largeFileLOC = 400

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
		pi.Dir = b.packageDir(p, rel)
		b.collectPackageImports(p, pi)

		b.collectFileMetrics(p, pi)
		pi.TestOnly = pi.ProdLOC == 0 && pi.TestLOC > 0

		b.idx.Packages[p.PkgPath] = pi
		if area != "" {
			m := b.module(area)
			m.Packages++
			m.TestLOC += pi.TestLOC
		}

		b.indexDefinitions(p, pi, false)
	}
}

func (b *builder) packageDir(p *packages.Package, rel string) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	if p.Module != nil && p.Module.Dir != "" {
		// Test-only packages have no production files; derive the directory
		// from the owning module so *_test.go metrics are still collected.
		return filepath.Join(p.Module.Dir, filepath.FromSlash(rel))
	}
	return ""
}

func (b *builder) collectPackageImports(p *packages.Package, pi *PkgInfo) {
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
	for _, p := range pkgs {
		pi, ok := b.pkgInfoFor(p)
		if !ok {
			continue
		}
		b.indexDefinitions(p, pi, true)
	}
}

func (b *builder) collectFileMetrics(p *packages.Package, pi *PkgInfo) {
	var mod *ModSummary
	if pi.ModuleArea != "" {
		mod = b.module(pi.ModuleArea)
	}
	for _, path := range p.GoFiles {
		b.collectProductionFileMetrics(path, pi, mod)
	}
	b.collectTestFileMetrics(pi)
}

func (b *builder) collectProductionFileMetrics(path string, pi *PkgInfo, mod *ModSummary) {
	facts, err := b.factsFor(path)
	if err != nil {
		return
	}
	rel := relFile(b.absRoot, path)
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
	if facts.generated {
		pi.GeneratedFiles++
		pi.GeneratedLOC += facts.loc
		if mod != nil {
			mod.GeneratedFiles++
			mod.GeneratedLOC += facts.loc
		}
		return
	}
	pi.ProdLOC += facts.loc
	if mod != nil {
		mod.ProdLOC += facts.loc
	}
	if facts.loc < largeFileLOC {
		return
	}
	metric := FileMetric{Path: rel, LOC: facts.loc, Generated: facts.generated, Untracked: untracked}
	b.idx.LargeFiles = append(b.idx.LargeFiles, metric)
	if mod != nil {
		mod.LargeFiles = append(mod.LargeFiles, metric)
	}
}

func (b *builder) collectTestFileMetrics(pi *PkgInfo) {
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
		if facts, err := b.factsFor(filepath.Join(pi.Dir, entry.Name())); err == nil {
			pi.TestLOC += facts.loc
		}
	}
}

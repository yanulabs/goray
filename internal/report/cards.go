package report

import (
	"sort"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func buildModuleCards(idx *index.Index, cached map[string]cache.Cached) []render.HTMLModuleCard {
	modules := idx.ModulesByLOC()
	out := make([]render.HTMLModuleCard, 0, len(modules))
	for _, module := range modules {
		id := "mod:" + module.Name
		out = append(out, render.HTMLModuleCard{
			ID: id, Name: module.Name, Packages: module.Packages,
			ProdLOC: module.ProdLOC, TestLOC: module.TestLOC, TestOnly: module.TestOnly,
			RootExported: module.RootExported, PortsCalls: module.PortsCalls,
			Goroutines: module.Goroutines, OutModules: len(module.OutModules), InModules: len(module.InModules),
			UntrackedFiles: module.UntrackedFiles, AllUntracked: module.AllUntracked,
			SyncState: stateForID(cached, id, idx.Summary.Get(id)), Summary: summaryView(cached[id]),
		})
	}
	return out
}

func buildPackageCards(idx *index.Index, cached map[string]cache.Cached) []render.HTMLPackageCard {
	pkgs := make([]*index.PkgInfo, 0, len(idx.Packages))
	for _, pkg := range idx.Packages {
		pkgs = append(pkgs, pkg)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].RelPath < pkgs[j].RelPath })
	out := make([]render.HTMLPackageCard, 0, len(pkgs))
	for _, pkg := range pkgs {
		id := "pkg:" + pkg.PkgPath
		out = append(out, render.HTMLPackageCard{
			ID: id, PkgPath: pkg.PkgPath, RelPath: pkg.RelPath, Module: pkg.ModuleArea,
			ProdLOC: pkg.ProdLOC, TestLOC: pkg.TestLOC, TestOnly: pkg.TestOnly, Exported: pkg.Exported,
			FanIn: len(idx.RevImports[pkg.PkgPath]), FanOut: len(pkg.Imports),
			GeneratedFiles: pkg.GeneratedFiles, UntrackedFiles: pkg.UntrackedFiles,
			SyncState: stateForID(cached, id, idx.Summary.Get(id)), Summary: summaryView(cached[id]),
		})
	}
	return out
}

func stateForID(cached map[string]cache.Cached, id string, summary *index.SummarySymbol) string {
	if item, ok := cached[id]; ok && item.State != "" {
		return string(item.State)
	}
	if summary == nil {
		return string(cache.StateMissing)
	}
	return stateFor(cached, summary)
}

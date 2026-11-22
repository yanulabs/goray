// Package report assembles the offline HTML architecture report.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

// Build combines an immutable index snapshot with cached summaries.
func Build(ctx context.Context, idx *index.Index, store *cache.Cache) (render.HTMLReport, error) {
	if idx == nil || idx.Summary == nil {
		return render.HTMLReport{}, fmt.Errorf("index has no summary metadata")
	}
	if store != nil {
		if err := store.Sync(ctx, idx); err != nil {
			return render.HTMLReport{}, fmt.Errorf("sync summary cache: %w", err)
		}
	}

	canonical := make(map[string]*index.Symbol)
	canonicalByExisting := make(map[string]string)
	for _, sym := range idx.Symbols {
		id := sym.CanonicalID()
		if id == "" {
			continue
		}
		canonical[id] = sym
		canonicalByExisting[sym.ID] = id
	}

	cached := map[string]cache.Cached{}
	if store != nil {
		for _, sym := range idx.Summary.Symbols {
			item, err := store.Lookup(ctx, sym.ID)
			if err != nil {
				return render.HTMLReport{}, err
			}
			cached[sym.ID] = item
		}
	}

	rows := make([]render.HTMLSymbol, 0, len(idx.Summary.Symbols))
	for _, summary := range idx.Summary.Symbols {
		sym := canonical[summary.ID]
		row := render.HTMLSymbol{
			ID: summary.ID, Name: symbolLabel(summary, sym), Kind: summary.Kind,
			PkgPath: summary.PkgPath, Module: summary.ModuleName, File: summary.FilePath,
			StartLine: summary.StartLine, EndLine: summary.EndLine,
			LOC: summary.LOC, Complexity: summary.Complexity, FanIn: summary.FanIn,
			FanOut: fanOut(idx, summary, sym), Exported: summary.Exported,
			Dead: summary.Dead, Eligible: summary.Eligible,
			IsTest: summary.IsTest, IsGenerated: summary.IsGenerated,
			Untracked:   untrackedFor(idx, summary, sym),
			TestCovered: testCoveredFor(idx, sym),
			SyncState:   stateFor(cached, summary),
			Signature:   signatureFor(summary, sym),
			Doc:         docFor(summary),
			Summary:     summaryView(cached[summary.ID]),
		}
		if sym != nil && (sym.Kind == "func" || sym.Kind == "method") {
			row.Callers = canonicalRefs(idx.Callers[sym.ID], canonicalByExisting)
			row.Callees = canonicalRefs(idx.Callees[sym.ID], canonicalByExisting)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	metrics := buildMetrics(idx, rows)
	coverage, err := buildCoverage(ctx, store)
	if err != nil {
		return render.HTMLReport{}, err
	}
	cycles := idx.ModuleCycles()
	if cycles == nil {
		cycles = [][]string{}
	}
	report := render.HTMLReport{
		Repo: idx.RootDir, ModulePath: idx.ModulePath,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Metrics:     metrics, Coverage: coverage,
		Hotspots:        render.BuildHotspots(rows),
		CoupledPackages: buildCoupling(idx),
		Modules:         buildModuleCards(idx, cached),
		Packages:        buildPackageCards(idx, cached),
		Symbols:         rows,
		Interfaces:      buildInterfaces(idx, canonicalByExisting),
		Graphs: render.HTMLGraphs{
			Modules:  render.LayoutGraph(buildModuleGraph(idx, cached)),
			Packages: render.LayoutGraph(buildPackageGraph(idx, cached)),
		},
		Tree:         buildTree(idx, cached, canonical),
		ModuleCycles: cycles,
	}
	return report, nil
}

func untrackedFor(idx *index.Index, summary *index.SummarySymbol, sym *index.Symbol) bool {
	if sym != nil {
		return sym.Untracked
	}
	return idx.UntrackedFiles[summary.FilePath]
}

func testCoveredFor(idx *index.Index, sym *index.Symbol) *bool {
	if sym == nil || (sym.Kind != "func" && sym.Kind != "method") {
		return nil
	}
	covered := idx.TestCovered[sym.ID]
	return &covered
}

func signatureFor(summary *index.SummarySymbol, sym *index.Symbol) string {
	if summary.Signature != "" {
		return summary.Signature
	}
	if sym != nil {
		return sym.Signature
	}
	return ""
}

func docFor(summary *index.SummarySymbol) string {
	return summary.Doc
}

func symbolLabel(summary *index.SummarySymbol, sym *index.Symbol) string {
	if sym != nil {
		return sym.Name
	}
	switch summary.Kind {
	case "module":
		return summary.ModuleName
	case "package":
		return summary.PkgPath
	case "file":
		return summary.FilePath
	}
	if i := strings.LastIndex(summary.ID, "."); i >= 0 {
		return summary.ID[i+1:]
	}
	return summary.ID
}

func fanOut(idx *index.Index, summary *index.SummarySymbol, sym *index.Symbol) int {
	if sym != nil {
		switch sym.Kind {
		case "func", "method":
			return len(idx.Callees[sym.ID])
		case "type", "interface", "struct":
			return len(idx.TypeImpls[sym.ID])
		}
	}
	switch summary.Kind {
	case "module":
		if mod := idx.Modules[summary.ModuleName]; mod != nil {
			return len(mod.OutModules)
		}
	case "package":
		if pkg := idx.Packages[summary.PkgPath]; pkg != nil {
			return len(pkg.Imports)
		}
	}
	return 0
}

func canonicalRefs(ids []string, canonicalByExisting map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if canonical := canonicalByExisting[id]; canonical != "" {
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}

func stateFor(cached map[string]cache.Cached, summary *index.SummarySymbol) string {
	if item, ok := cached[summary.ID]; ok && item.State != "" {
		return string(item.State)
	}
	if !summary.Eligible {
		return string(cache.StateNotEligible)
	}
	return string(cache.StateMissing)
}

func summaryView(item cache.Cached) *render.HTMLSummary {
	if len(item.Summary) == 0 {
		return nil
	}
	var payload struct {
		Intent        string   `json:"intent"`
		SideEffects   []string `json:"side_effects"`
		Invariants    []string `json:"invariants"`
		Concurrency   string   `json:"concurrency"`
		KeyInvariants []string `json:"key_invariants"`
		Boundaries    []string `json:"boundaries"`
	}
	if err := json.Unmarshal(item.Summary, &payload); err != nil {
		return nil
	}
	return &render.HTMLSummary{
		Intent: payload.Intent, SideEffects: payload.SideEffects,
		Invariants: payload.Invariants, Concurrency: payload.Concurrency,
		KeyInvariants: payload.KeyInvariants, Boundaries: payload.Boundaries,
		SyncState: string(item.State), ModelID: item.ModelID, DepsChanged: item.DepsChanged,
	}
}

func buildMetrics(idx *index.Index, rows []render.HTMLSymbol) render.HTMLMetrics {
	metrics := render.HTMLMetrics{Modules: len(idx.Modules), Packages: len(idx.Packages)}
	for _, pkg := range idx.Packages {
		metrics.LOC += pkg.ProdLOC
	}
	for _, row := range rows {
		switch row.Kind {
		case "file":
			metrics.Files++
		case "type":
			metrics.Types++
		case "func":
			metrics.Funcs++
		case "method":
			metrics.Methods++
		}
		if row.Dead {
			metrics.Dead++
		}
	}
	return metrics
}

func buildCoverage(ctx context.Context, store *cache.Cache) (render.HTMLCoverage, error) {
	if store == nil {
		return render.HTMLCoverage{}, nil
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		return render.HTMLCoverage{}, err
	}
	states := stats.SummaryStates
	out := render.HTMLCoverage{
		Eligible: stats.EligibleSymbols, Synced: states["synced"],
		SoftStale: states["soft_stale"], HardStale: states["hard_stale"],
		Missing: states["missing"], NotEligible: stats.Tables["working_symbols"] - stats.EligibleSymbols,
	}
	if out.Eligible > 0 {
		out.Percent = float64(out.Synced) / float64(out.Eligible) * 100
		out.Percent = float64(int(out.Percent*10+0.5)) / 10
	}
	return out, nil
}

func buildCoupling(idx *index.Index) []render.HTMLCoupled {
	out := make([]render.HTMLCoupled, 0, len(idx.Packages))
	for pkgPath, pkg := range idx.Packages {
		out = append(out, render.HTMLCoupled{
			Pkg: pkgPath, FanIn: len(idx.RevImports[pkgPath]), FanOut: len(pkg.Imports),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].FanIn+out[i].FanOut, out[j].FanIn+out[j].FanOut
		if left != right {
			return left > right
		}
		return out[i].Pkg < out[j].Pkg
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func buildModuleCards(idx *index.Index, cached map[string]cache.Cached) []render.HTMLModuleCard {
	modules := idx.ModulesByLOC()
	out := make([]render.HTMLModuleCard, 0, len(modules))
	for _, module := range modules {
		id := "mod:" + module.Name
		out = append(out, render.HTMLModuleCard{
			ID: id, Name: module.Name, Packages: module.Packages,
			ProdLOC: module.ProdLOC, TestLOC: module.TestLOC,
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
			ProdLOC: pkg.ProdLOC, TestLOC: pkg.TestLOC, Exported: pkg.Exported,
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

func buildModuleGraph(idx *index.Index, cached map[string]cache.Cached) render.HTMLGraph {
	graph := render.HTMLGraph{Nodes: make([]render.HTMLGraphNode, 0, len(idx.Modules)), Edges: make([]render.HTMLGraphEdge, 0)}
	for _, name := range idx.SortedModuleNames() {
		module := idx.Modules[name]
		id := "mod:" + name
		summary := idx.Summary.Get(id)
		node := render.HTMLGraphNode{
			ID: id, Label: name, Kind: "module", Module: name, LOC: module.ProdLOC,
			FanIn: len(module.InModules), FanOut: len(module.OutModules),
			Eligible:  summary != nil && summary.Eligible,
			Untracked: module.UntrackedFiles > 0,
			SyncState: stateForID(cached, id, summary), Summary: summaryView(cached[id]),
		}
		graph.Nodes = append(graph.Nodes, node)
		for _, target := range sortedKeys(module.OutModules) {
			graph.Edges = append(graph.Edges, render.HTMLGraphEdge{From: id, To: "mod:" + target, Weight: len(module.OutModules[target])})
		}
	}
	return graph
}

func buildPackageGraph(idx *index.Index, cached map[string]cache.Cached) render.HTMLGraph {
	graph := render.HTMLGraph{Nodes: make([]render.HTMLGraphNode, 0, len(idx.Packages)), Edges: make([]render.HTMLGraphEdge, 0)}
	paths := make([]string, 0, len(idx.Packages))
	for pkgPath := range idx.Packages {
		paths = append(paths, pkgPath)
	}
	sort.Strings(paths)
	for _, pkgPath := range paths {
		pkg := idx.Packages[pkgPath]
		id := "pkg:" + pkgPath
		summary := idx.Summary.Get(id)
		node := render.HTMLGraphNode{
			ID: id, Label: pkg.RelPath, Kind: "package", Module: pkg.ModuleArea, LOC: pkg.ProdLOC,
			FanIn: len(idx.RevImports[pkgPath]), FanOut: len(pkg.Imports),
			Eligible:  summary != nil && summary.Eligible,
			Untracked: pkg.UntrackedFiles > 0,
			SyncState: stateForID(cached, id, summary), Summary: summaryView(cached[id]),
		}
		graph.Nodes = append(graph.Nodes, node)
		for _, imported := range pkg.Imports {
			graph.Edges = append(graph.Edges, render.HTMLGraphEdge{From: id, To: "pkg:" + imported, Weight: 1})
		}
	}
	return graph
}

func buildInterfaces(idx *index.Index, canonicalByExisting map[string]string) []render.HTMLInterface {
	out := make([]render.HTMLInterface, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "interface" {
			continue
		}
		id := sym.CanonicalID()
		if id == "" {
			continue
		}
		iface := render.HTMLInterface{
			ID: id, Name: sym.Name, PkgPath: sym.PkgPath,
			Module: sym.ModuleArea, Methods: sym.Methods,
			Implementations: make([]render.HTMLImpl, 0),
		}
		for _, match := range idx.IfaceImpls[sym.ID] {
			impl := idx.Symbols[match.TypeID]
			if impl == nil {
				continue
			}
			implID := canonicalByExisting[impl.ID]
			if implID == "" {
				continue
			}
			iface.Implementations = append(iface.Implementations, render.HTMLImpl{
				ID: implID, Name: impl.Name, PkgPath: impl.PkgPath, ViaPtr: match.ViaPtr,
			})
		}
		sort.Slice(iface.Implementations, func(i, j int) bool {
			if iface.Implementations[i].PkgPath != iface.Implementations[j].PkgPath {
				return iface.Implementations[i].PkgPath < iface.Implementations[j].PkgPath
			}
			return iface.Implementations[i].Name < iface.Implementations[j].Name
		})
		out = append(out, iface)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PkgPath != out[j].PkgPath {
			return out[i].PkgPath < out[j].PkgPath
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func buildTree(idx *index.Index, cached map[string]cache.Cached, canonical map[string]*index.Symbol) []*render.HTMLTreeNode {
	byID := make(map[string]*render.HTMLTreeNode, len(idx.Summary.Symbols))
	for _, summary := range idx.Summary.Symbols {
		kind := summary.Kind
		if kind == "type" {
			if sym := canonical[summary.ID]; sym != nil && (sym.Kind == "interface" || sym.Kind == "struct") {
				kind = sym.Kind
			}
		}
		switch kind {
		case "package", "module", "file", "type", "struct", "interface", "func", "method":
			byID[summary.ID] = &render.HTMLTreeNode{
				ID: summary.ID, Kind: kind,
				Label:     symbolLabel(summary, canonical[summary.ID]),
				SyncState: stateFor(cached, summary),
			}
		}
	}
	roots := make([]*render.HTMLTreeNode, 0)
	for _, summary := range idx.Summary.Symbols {
		node := byID[summary.ID]
		if node == nil {
			continue
		}
		if parent := byID[summary.ParentID]; parent != nil {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	var sortNodes func([]*render.HTMLTreeNode)
	sortNodes = func(nodes []*render.HTMLTreeNode) {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Kind != nodes[j].Kind {
				return kindRank(nodes[i].Kind) < kindRank(nodes[j].Kind)
			}
			return nodes[i].Label < nodes[j].Label
		})
		for _, node := range nodes {
			sortNodes(node.Children)
		}
	}
	sortNodes(roots)
	return roots
}

func kindRank(kind string) int {
	switch kind {
	case "module":
		return 0
	case "package":
		return 1
	case "file":
		return 2
	case "type", "struct", "interface":
		return 3
	case "func", "method":
		return 4
	}
	return 5
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

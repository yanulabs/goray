package index

import (
	"sort"
	"strings"
)

// ModuleCycles returns cross-module dependency cycles as node sequences whose
// first and last element are identical (for example a -> b -> a). Go forbids
// package cycles, so module-group cycles are a high-signal architectural smell.
// Cycles are enumerated with Johnson's algorithm over the SCC decomposition so
// no elementary cycle is lost, and the output order is canonical.
func (idx *Index) ModuleCycles() [][]string {
	adj := map[string][]string{}
	nodeSet := map[string]bool{}
	for name, mod := range idx.Modules {
		nodeSet[name] = true
		for dst := range mod.OutModules {
			nodeSet[dst] = true
			adj[name] = append(adj[name], dst)
		}
	}
	for _, neighbors := range adj {
		sort.Strings(neighbors)
	}
	nodes := make([]string, 0, len(nodeSet))
	for name := range nodeSet {
		nodes = append(nodes, name)
	}
	sort.Strings(nodes)
	pos := make(map[string]int, len(nodes))
	for i, name := range nodes {
		pos[name] = i
	}

	seen := map[string]bool{}
	var cycles [][]string
	for _, component := range stronglyConnectedComponents(nodes, adj) {
		if !nontrivialComponent(component, adj) {
			continue
		}
		sort.Slice(component, func(i, j int) bool { return pos[component[i]] < pos[component[j]] })
		for startIndex := range component {
			start := component[startIndex]
			allowed := make(map[string]bool, len(component)-startIndex)
			for _, name := range component[startIndex:] {
				allowed[name] = true
			}
			for _, cycle := range johnsonCircuits(start, adj, allowed) {
				key := cycleKey(cycle[:len(cycle)-1])
				if seen[key] {
					continue
				}
				seen[key] = true
				cycles = append(cycles, cycle)
			}
		}
	}
	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], ">") < strings.Join(cycles[j], ">")
	})
	return cycles
}

// stronglyConnectedComponents is Tarjan's algorithm. Components are sorted
// lexically so later passes are deterministic.
func stronglyConnectedComponents(nodes []string, adj map[string][]string) [][]string {
	indices := make(map[string]int, len(nodes))
	lowlink := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	stack := make([]string, 0, len(nodes))
	next := 0
	var components [][]string

	var strongConnect func(string)
	strongConnect = func(v string) {
		indices[v] = next
		lowlink[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := indices[w]; !seen {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
				continue
			}
			if onStack[w] && indices[w] < lowlink[v] {
				lowlink[v] = indices[w]
			}
		}
		if lowlink[v] != indices[v] {
			return
		}
		component := make([]string, 0)
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}

	for _, v := range nodes {
		if _, seen := indices[v]; !seen {
			strongConnect(v)
		}
	}
	sort.Slice(components, func(i, j int) bool {
		return strings.Join(components[i], "\x00") < strings.Join(components[j], "\x00")
	})
	return components
}

func nontrivialComponent(component []string, adj map[string][]string) bool {
	if len(component) > 1 {
		return true
	}
	if len(component) == 1 {
		for _, next := range adj[component[0]] {
			if next == component[0] {
				return true
			}
		}
	}
	return false
}

// johnsonCircuits enumerates elementary circuits through start in the induced
// subgraph allowed, using Johnson's blocked-set pruning.
func johnsonCircuits(start string, adj map[string][]string, allowed map[string]bool) [][]string {
	blocked := map[string]bool{}
	blockedBy := map[string]map[string]bool{}
	stack := make([]string, 0, len(allowed))
	var cycles [][]string

	var unblock func(string)
	unblock = func(node string) {
		blocked[node] = false
		for parent := range blockedBy[node] {
			delete(blockedBy[node], parent)
			if blocked[parent] {
				unblock(parent)
			}
		}
	}

	var circuit func(string) bool
	circuit = func(v string) bool {
		found := false
		stack = append(stack, v)
		blocked[v] = true
		for _, w := range adj[v] {
			if !allowed[w] {
				continue
			}
			if w == start {
				cycle := append(append([]string{}, stack...), start)
				cycles = append(cycles, cycle)
				found = true
				continue
			}
			if blocked[w] {
				continue
			}
			if circuit(w) {
				found = true
			}
		}
		if found {
			unblock(v)
		} else {
			for _, w := range adj[v] {
				if !allowed[w] {
					continue
				}
				if blockedBy[w] == nil {
					blockedBy[w] = map[string]bool{}
				}
				blockedBy[w][v] = true
			}
		}
		stack = stack[:len(stack)-1]
		return found
	}

	circuit(start)
	return cycles
}

func cycleKey(nodes []string) string {
	best := append([]string{}, nodes...)
	for i := 1; i < len(nodes); i++ {
		candidate := append(append([]string{}, nodes[i:]...), nodes[:i]...)
		if strings.Join(candidate, "\x00") < strings.Join(best, "\x00") {
			best = candidate
		}
	}
	return strings.Join(best, "\x00")
}

// TrackedView returns a shallow snapshot with untracked files and their
// symbols removed. It is used by tools that honor tracked_only; the original
// index is never mutated and untracked files remain in the semantic index.
// File-level metrics and module dependency edges are recomputed from tracked
// summaries and references so partially untracked packages do not over-report
// size, generated code, exports, or cross-module links.
func (idx *Index) TrackedView() *Index {
	if len(idx.UntrackedFiles) == 0 {
		return idx
	}
	out := *idx
	out.Symbols, out.SymbolsByName, out.CanonicalSymbols = trackedSymbols(idx)
	out.LargeFiles = trackedLargeFiles(idx.LargeFiles)
	fileStats, haveFileStats := idx.trackedFileStats()
	out.Modules = trackedModuleShells(idx.Modules)
	out.Packages = trackedPackages(idx.Packages, fileStats, haveFileStats)
	applyTrackedSymbolMetrics(&out)
	aggregateTrackedModules(&out)
	recomputeTrackedModuleEdges(&out, idx.UntrackedFiles)
	return &out
}

func trackedSymbols(idx *Index) (map[string]*Symbol, map[string][]*Symbol, map[string]*Symbol) {
	symbols := make(map[string]*Symbol, len(idx.Symbols))
	byName := make(map[string][]*Symbol, len(idx.SymbolsByName))
	for id, sym := range idx.Symbols {
		if sym.Untracked {
			continue
		}
		symbols[id] = sym
		key := strings.ToLower(sym.Name)
		byName[key] = append(byName[key], sym)
	}
	canonical := make(map[string]*Symbol, len(idx.CanonicalSymbols))
	for id, sym := range idx.CanonicalSymbols {
		if !sym.Untracked {
			canonical[id] = sym
		}
	}
	return symbols, byName, canonical
}

func trackedLargeFiles(files []FileMetric) []FileMetric {
	out := make([]FileMetric, 0, len(files))
	for _, file := range files {
		if !file.Untracked {
			out = append(out, file)
		}
	}
	return out
}

func trackedModuleShells(modules map[string]*ModSummary) map[string]*ModSummary {
	out := make(map[string]*ModSummary, len(modules))
	for name, mod := range modules {
		if mod.AllUntracked {
			continue
		}
		copyMod := *mod
		copyMod.UntrackedFiles = 0
		copyMod.AllUntracked = false
		copyMod.Packages = 0
		copyMod.Files = 0
		copyMod.GeneratedFiles = 0
		copyMod.GeneratedSymbols = 0
		copyMod.GeneratedLOC = 0
		copyMod.ProdLOC = 0
		copyMod.TestLOC = 0
		copyMod.RootExported = 0
		copyMod.TestOnly = false
		copyMod.LargeFiles = trackedLargeFiles(mod.LargeFiles)
		copyMod.OutModules = map[string][]string{}
		copyMod.InModules = map[string][]string{}
		out[name] = &copyMod
	}
	return out
}

func trackedPackages(packages map[string]*PkgInfo, fileStats map[string][]trackedFile, haveFileStats bool) map[string]*PkgInfo {
	out := make(map[string]*PkgInfo, len(packages))
	for path, pkg := range packages {
		copyPkg := *pkg
		copyPkg.UntrackedFiles = 0
		copyPkg.Exported = 0
		copyPkg.GeneratedSymbols = 0
		if haveFileStats {
			applyTrackedFileMetrics(&copyPkg, fileStats[path])
		} else if copyPkg.Files > pkg.UntrackedFiles {
			copyPkg.Files -= pkg.UntrackedFiles
		}
		out[path] = &copyPkg
	}
	return out
}

func applyTrackedFileMetrics(pkg *PkgInfo, files []trackedFile) {
	pkg.Files = 0
	pkg.ProdFiles = 0
	pkg.TestFiles = 0
	pkg.ProdLOC = 0
	pkg.TestLOC = 0
	pkg.GeneratedFiles = 0
	pkg.GeneratedLOC = 0
	for _, file := range files {
		if file.test {
			pkg.TestFiles++
			pkg.TestLOC += file.loc
			continue
		}
		pkg.Files++
		pkg.ProdFiles++
		if file.generated {
			pkg.GeneratedFiles++
			pkg.GeneratedLOC += file.loc
			continue
		}
		pkg.ProdLOC += file.loc
	}
	pkg.TestOnly = pkg.ProdLOC == 0 && pkg.TestLOC > 0
}

func applyTrackedSymbolMetrics(idx *Index) {
	for _, sym := range idx.Symbols {
		if sym.IsTest {
			continue
		}
		pkg := idx.Packages[sym.PkgPath]
		if pkg == nil {
			continue
		}
		if sym.Generated {
			pkg.GeneratedSymbols++
		}
		if sym.Exported {
			pkg.Exported++
		}
	}
}

func aggregateTrackedModules(idx *Index) {
	for _, pkg := range idx.Packages {
		mod := idx.Modules[pkg.ModuleArea]
		if mod == nil {
			continue
		}
		mod.Packages++
		mod.Files += pkg.Files
		mod.GeneratedFiles += pkg.GeneratedFiles
		mod.GeneratedSymbols += pkg.GeneratedSymbols
		mod.GeneratedLOC += pkg.GeneratedLOC
		mod.ProdLOC += pkg.ProdLOC
		mod.TestLOC += pkg.TestLOC
		if isModuleRoot(pkg.RelPath) {
			mod.RootExported += pkg.Exported
		}
	}
	for _, mod := range idx.Modules {
		mod.TestOnly = mod.ProdLOC == 0 && mod.TestLOC > 0
	}
}

func recomputeTrackedModuleEdges(idx *Index, untracked map[string]bool) {
	type edgeSet map[string]map[string]map[string]struct{}
	outModules := edgeSet{}
	inModules := edgeSet{}
	for targetID, refs := range idx.XRefs {
		target := idx.Symbols[targetID]
		if target == nil || target.ModuleArea == "" {
			continue
		}
		for _, ref := range refs {
			if ref.Test || untracked[ref.File] {
				continue
			}
			srcMod := trackedRefModule(idx, ref)
			if srcMod == "" || srcMod == target.ModuleArea || idx.Modules[srcMod] == nil {
				continue
			}
			addNested(outModules, srcMod, target.ModuleArea, target.Name)
			addNested(inModules, target.ModuleArea, srcMod, target.Name)
		}
	}
	for src, targets := range outModules {
		mod := idx.Modules[src]
		if mod == nil {
			continue
		}
		for dst, symbols := range targets {
			mod.OutModules[dst] = sortedKeys(symbols)
		}
	}
	for dst, sources := range inModules {
		mod := idx.Modules[dst]
		if mod == nil {
			continue
		}
		for src, symbols := range sources {
			mod.InModules[src] = sortedKeys(symbols)
		}
	}
}

func trackedRefModule(idx *Index, ref XRef) string {
	if ref.FromMod != "" {
		return ref.FromMod
	}
	if pi := idx.Packages[ref.FromPkg]; pi != nil {
		return pi.ModuleArea
	}
	if pi := idx.Packages[strings.TrimSuffix(ref.FromPkg, "_test")]; pi != nil {
		return pi.ModuleArea
	}
	return ""
}

type trackedFile struct {
	loc       int
	test      bool
	generated bool
}

func (idx *Index) trackedFileStats() (map[string][]trackedFile, bool) {
	if idx.Summary == nil {
		return nil, false
	}
	stats := map[string][]trackedFile{}
	found := false
	for _, sym := range idx.Summary.Symbols {
		if sym.Kind != "file" || sym.FilePath == "" || sym.PkgPath == "" {
			continue
		}
		if idx.UntrackedFiles[sym.FilePath] {
			continue
		}
		stats[sym.PkgPath] = append(stats[sym.PkgPath], trackedFile{
			loc: sym.LOC, test: sym.IsTest, generated: sym.IsGenerated,
		})
		found = true
	}
	return stats, found
}

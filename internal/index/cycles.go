package index

import (
	"sort"
	"strings"
)

// ModuleCycles returns cross-module dependency cycles as node sequences whose
// first and last element are identical (for example a -> b -> a). Go forbids
// package cycles, so module-group cycles are a high-signal architectural smell.
func (idx *Index) ModuleCycles() [][]string {
	adj := map[string][]string{}
	for name, mod := range idx.Modules {
		for dst := range mod.OutModules {
			adj[name] = append(adj[name], dst)
		}
	}
	for _, neighbors := range adj {
		sort.Strings(neighbors)
	}

	seen := map[string]bool{}
	var cycles [][]string
	var visit func(node string, path []string, onPath map[string]bool, explored map[string]bool)
	visit = func(node string, path []string, onPath map[string]bool, explored map[string]bool) {
		for _, next := range adj[node] {
			edge := node + ">" + next
			if explored[edge] {
				continue
			}
			explored[edge] = true
			if onPath[next] {
				start := 0
				for i, name := range path {
					if name == next {
						start = i
						break
					}
				}
				cycle := append(append([]string{}, path[start:]...), next)
				key := cycleKey(cycle[:len(cycle)-1])
				if !seen[key] {
					seen[key] = true
					cycles = append(cycles, cycle)
				}
				continue
			}
			if len(path) > len(adj) {
				continue
			}
			onPath[next] = true
			visit(next, append(path, next), onPath, explored)
			delete(onPath, next)
		}
	}
	for _, start := range idx.SortedModuleNames() {
		visit(start, []string{start}, map[string]bool{start: true}, map[string]bool{})
	}
	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], ">") < strings.Join(cycles[j], ">")
	})
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
func (idx *Index) TrackedView() *Index {
	if len(idx.UntrackedFiles) == 0 {
		return idx
	}
	out := *idx
	out.Symbols = make(map[string]*Symbol, len(idx.Symbols))
	out.SymbolsByName = make(map[string][]*Symbol, len(idx.SymbolsByName))
	for id, sym := range idx.Symbols {
		if sym.Untracked {
			continue
		}
		out.Symbols[id] = sym
		key := strings.ToLower(sym.Name)
		out.SymbolsByName[key] = append(out.SymbolsByName[key], sym)
	}
	out.CanonicalSymbols = make(map[string]*Symbol, len(idx.CanonicalSymbols))
	for id, sym := range idx.CanonicalSymbols {
		if !sym.Untracked {
			out.CanonicalSymbols[id] = sym
		}
	}
	out.LargeFiles = make([]FileMetric, 0, len(idx.LargeFiles))
	for _, file := range idx.LargeFiles {
		if !file.Untracked {
			out.LargeFiles = append(out.LargeFiles, file)
		}
	}
	out.Modules = make(map[string]*ModSummary, len(idx.Modules))
	for name, mod := range idx.Modules {
		if mod.AllUntracked {
			continue
		}
		copyMod := *mod
		copyMod.UntrackedFiles = 0
		copyMod.AllUntracked = false
		if copyMod.Files > mod.UntrackedFiles {
			copyMod.Files -= mod.UntrackedFiles
		}
		copyMod.LargeFiles = make([]FileMetric, 0, len(mod.LargeFiles))
		for _, file := range mod.LargeFiles {
			if !file.Untracked {
				copyMod.LargeFiles = append(copyMod.LargeFiles, file)
			}
		}
		out.Modules[name] = &copyMod
	}
	out.Packages = make(map[string]*PkgInfo, len(idx.Packages))
	for path, pkg := range idx.Packages {
		copyPkg := *pkg
		copyPkg.UntrackedFiles = 0
		if copyPkg.Files > pkg.UntrackedFiles {
			copyPkg.Files -= pkg.UntrackedFiles
		}
		out.Packages[path] = &copyPkg
	}
	return &out
}

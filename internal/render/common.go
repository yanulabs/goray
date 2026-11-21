package render

import (
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

func safeID(s string) string {
	return strings.NewReplacer("-", "_", "/", "_", ".", "_").Replace(s)
}

func limit[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

type moduleEdge struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Symbols []string `json:"symbols"`
}

func moduleEdges(idx *index.Index) []moduleEdge {
	edges := make([]moduleEdge, 0)
	for _, name := range idx.SortedModuleNames() {
		mod := idx.Modules[name]
		for _, dst := range sortedMapKeys(mod.OutModules) {
			symbols := append([]string{}, mod.OutModules[dst]...)
			sort.Strings(symbols)
			edges = append(edges, moduleEdge{From: name, To: dst, Symbols: symbols})
		}
	}
	return edges
}

type codebaseHotspot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Module     string `json:"module"`
	LOC        int    `json:"loc"`
	Complexity int    `json:"complexity"`
	FanIn      int    `json:"fan_in"`
	RiskScore  int    `json:"risk_score"`
	Untracked  bool   `json:"untracked,omitempty"`
}

func (h codebaseHotspot) displayFile() string {
	if h.Untracked {
		return h.File + " [untracked]"
	}
	return h.File
}

func codebaseHotspots(idx *index.Index, n int) []codebaseHotspot {
	items := make([]codebaseHotspot, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "func" && sym.Kind != "method" {
			continue
		}
		if sym.IsTest || sym.Generated {
			continue
		}
		fanIn := len(idx.Callers[sym.ID])
		items = append(items, codebaseHotspot{
			ID: sym.ID, Name: sym.Name, File: sym.File, Line: sym.Line,
			Module: sym.ModuleArea, LOC: sym.LOC, Complexity: sym.Cyclomatic,
			FanIn: fanIn, RiskScore: sym.Cyclomatic*2 + fanIn, Untracked: sym.Untracked,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RiskScore != items[j].RiskScore {
			return items[i].RiskScore > items[j].RiskScore
		}
		return items[i].ID < items[j].ID
	})
	return limit(items, n)
}

func generatedFileCount(idx *index.Index) int {
	total := 0
	for _, pkg := range idx.Packages {
		total += pkg.GeneratedFiles
	}
	return total
}

func untrackedFileCount(idx *index.Index) int {
	total := 0
	for _, pkg := range idx.Packages {
		total += pkg.UntrackedFiles
	}
	return total
}

func modulePackages(idx *index.Index, name string) []*index.PkgInfo {
	packages := make([]*index.PkgInfo, 0)
	for _, pkg := range idx.Packages {
		if pkg.ModuleArea == name {
			packages = append(packages, pkg)
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].RelPath < packages[j].RelPath })
	return packages
}

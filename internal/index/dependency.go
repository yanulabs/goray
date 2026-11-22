package index

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DependencyExplanation is the exact evidence behind a from -> to edge.
type DependencyExplanation struct {
	From        string                `json:"from"`
	To          string                `json:"to"`
	FromKind    string                `json:"from_kind"`
	ToKind      string                `json:"to_kind"`
	IncludeTest bool                  `json:"include_tests"`
	Edges       []DependencyEdgeGroup `json:"edges"`
	Summary     DependencySummary     `json:"summary"`
	Hint        string                `json:"hint,omitempty"`
	Limit       int                   `json:"limit,omitempty"`
	Truncated   bool                  `json:"truncated,omitempty"`
	IndexState  string                `json:"-"`
}

// DependencyEdgeGroup aggregates one package pair.
type DependencyEdgeGroup struct {
	FromPkg string             `json:"from_pkg"`
	ToPkg   string             `json:"to_pkg"`
	Files   []DependencyFile   `json:"files"`
	Symbols []DependencySymbol `json:"symbols"`
}

// DependencyFile is one import site.
type DependencyFile struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Alias string `json:"alias,omitempty"`
	Blank bool   `json:"blank,omitempty"`
	Dot   bool   `json:"dot,omitempty"`
	Test  bool   `json:"test,omitempty"`
}

// DependencySymbol is one target symbol referenced through the edge.
type DependencySymbol struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	ID          string            `json:"id"`
	CanonicalID string            `json:"canonical_id,omitempty"`
	PkgPath     string            `json:"pkg_path"`
	Usages      []DependencyUsage `json:"usages"`
}

// DependencyUsage is one type-checked use site.
type DependencyUsage struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	FromFuncID string `json:"from_func_id,omitempty"`
	FromFunc   string `json:"from_func,omitempty"`
	Test       bool   `json:"test,omitempty"`
}

// DependencySummary holds the requested counters.
type DependencySummary struct {
	Files         int `json:"files"`
	TargetSymbols int `json:"target_symbols"`
	UsageSites    int `json:"usage_sites"`
}

type dependencyGroupKey struct {
	from string
	to   string
}

type dependencyGroups struct {
	groups     map[dependencyGroupKey]*DependencyEdgeGroup
	files      map[string]struct{}
	totalUsage int
}

func newDependencyGroups() *dependencyGroups {
	return &dependencyGroups{
		groups: map[dependencyGroupKey]*DependencyEdgeGroup{},
		files:  map[string]struct{}{},
	}
}

func (g *dependencyGroups) groupFor(from, to string) *DependencyEdgeGroup {
	key := dependencyGroupKey{from: from, to: to}
	if group := g.groups[key]; group != nil {
		return group
	}
	group := &DependencyEdgeGroup{FromPkg: from, ToPkg: to}
	g.groups[key] = group
	return group
}

func (g *dependencyGroups) addImports(idx *Index, fromPkgs, toPkgs map[string]bool, includeTests bool) {
	for _, site := range idx.ImportSites {
		if !fromPkgs[site.FromPkg] || !toPkgs[site.ToPkg] {
			continue
		}
		if site.Test && !includeTests {
			continue
		}
		group := g.groupFor(site.FromPkg, site.ToPkg)
		group.Files = append(group.Files, DependencyFile{
			File: site.File, Line: site.Line, Alias: site.Alias,
			Blank: site.Blank, Dot: site.Dot, Test: site.Test,
		})
		g.files[site.File] = struct{}{}
	}
}

func (g *dependencyGroups) addUsages(idx *Index, fromPkgs, toPkgs map[string]bool, includeTests bool) {
	// Attribute each usage to the referencing package so module-level queries
	// aggregate the correct package pair.
	usagesBySymbol := map[string][]DependencyUsage{}
	for targetID, refs := range idx.XRefs {
		sym := idx.Symbols[targetID]
		if sym == nil || !toPkgs[sym.PkgPath] {
			continue
		}
		for _, ref := range refs {
			if !fromPkgs[ref.FromPkg] || (ref.Test && !includeTests) {
				continue
			}
			key := ref.FromPkg + "\x00" + targetID
			usagesBySymbol[key] = append(usagesBySymbol[key], DependencyUsage{
				File: ref.File, Line: ref.Line, FromFuncID: ref.FromFuncID,
				FromFunc: funcName(idx, ref.FromFuncID), Test: ref.Test,
			})
			g.totalUsage++
		}
	}
	for key, usages := range usagesBySymbol {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		fromPkg, targetID := parts[0], parts[1]
		sym := idx.Symbols[targetID]
		if sym == nil {
			continue
		}
		group := g.groupFor(fromPkg, sym.PkgPath)
		canonical := sym.CanonicalID()
		if canonical == "" {
			canonical = sym.ID
		}
		group.Symbols = append(group.Symbols, DependencySymbol{
			Name: sym.Name, Kind: sym.Kind, ID: sym.ID, CanonicalID: canonical,
			PkgPath: sym.PkgPath, Usages: usages,
		})
	}
}

func (g *dependencyGroups) sortedEdges() []DependencyEdgeGroup {
	edges := make([]DependencyEdgeGroup, 0, len(g.groups))
	for _, group := range g.groups {
		sort.Slice(group.Files, func(i, j int) bool {
			if group.Files[i].File != group.Files[j].File {
				return group.Files[i].File < group.Files[j].File
			}
			return group.Files[i].Line < group.Files[j].Line
		})
		sort.Slice(group.Symbols, func(i, j int) bool { return group.Symbols[i].ID < group.Symbols[j].ID })
		edges = append(edges, *group)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromPkg != edges[j].FromPkg {
			return edges[i].FromPkg < edges[j].FromPkg
		}
		return edges[i].ToPkg < edges[j].ToPkg
	})
	return edges
}

func reverseDependencyHint(idx *Index, from, to string, fromPkgs, toPkgs map[string]bool, includeTests bool) string {
	reverseFiles := 0
	for _, site := range idx.ImportSites {
		if toPkgs[site.FromPkg] && fromPkgs[site.ToPkg] && (includeTests || !site.Test) {
			reverseFiles++
		}
	}
	if reverseFiles == 0 {
		return ""
	}
	return fmt.Sprintf("no %s -> %s edge; the reverse edge %s -> %s exists (%d import sites)", from, to, to, from, reverseFiles)
}

func applyDependencyLimit(explanation *DependencyExplanation) {
	if explanation.Limit <= 0 {
		return
	}
	remaining := explanation.Limit
	for i := range explanation.Edges {
		group := &explanation.Edges[i]
		kept := group.Symbols[:0]
		for _, sym := range group.Symbols {
			if remaining <= 0 {
				explanation.Truncated = true
				break
			}
			if len(sym.Usages) > remaining {
				sym.Usages = sym.Usages[:remaining]
				remaining = 0
				explanation.Truncated = true
				kept = append(kept, sym)
				break
			}
			remaining -= len(sym.Usages)
			kept = append(kept, sym)
		}
		if len(kept) < len(group.Symbols) {
			explanation.Truncated = true
		}
		group.Symbols = kept
	}
}

// ExplainDependency resolves both targets to package sets, then enumerates
// every import and symbol usage behind the resulting package-to-package edges.
func (idx *Index) ExplainDependency(from, to string, includeTests bool, limit int) (DependencyExplanation, error) {
	fromPkgs, fromKind, err := idx.resolveDependencyTarget(from)
	if err != nil {
		return DependencyExplanation{}, err
	}
	toPkgs, toKind, err := idx.resolveDependencyTarget(to)
	if err != nil {
		return DependencyExplanation{}, err
	}

	groups := newDependencyGroups()
	groups.addImports(idx, fromPkgs, toPkgs, includeTests)
	groups.addUsages(idx, fromPkgs, toPkgs, includeTests)

	explanation := DependencyExplanation{
		From: from, To: to, FromKind: fromKind, ToKind: toKind,
		IncludeTest: includeTests, Limit: limit,
	}
	explanation.Edges = groups.sortedEdges()

	symbolSet := map[string]struct{}{}
	for _, group := range explanation.Edges {
		for _, sym := range group.Symbols {
			symbolSet[sym.ID] = struct{}{}
		}
	}
	explanation.Summary = DependencySummary{
		Files:         len(groups.files),
		TargetSymbols: len(symbolSet),
		UsageSites:    groups.totalUsage,
	}
	if len(explanation.Edges) == 0 {
		explanation.Hint = reverseDependencyHint(idx, from, to, fromPkgs, toPkgs, includeTests)
	}
	applyDependencyLimit(&explanation)
	return explanation, nil
}

func funcName(idx *Index, id string) string {
	if sym := idx.Symbols[id]; sym != nil {
		return sym.Name
	}
	return ""
}

// resolveDependencyTarget resolves a full import path as a package and a
// module-group name as every package in that module. For a relative name that
// is both (e.g. "internal/index"), the module interpretation wins; pass the
// full import path to force a single package.
func (idx *Index) resolveDependencyTarget(arg string) (map[string]bool, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, "", errors.New("dependency target must not be empty")
	}
	if strings.HasPrefix(arg, "mod:") {
		arg = strings.TrimPrefix(arg, "mod:")
	} else if strings.HasPrefix(arg, "pkg:") {
		arg = strings.TrimPrefix(arg, "pkg:")
	}
	fullPath := false
	for _, mod := range idx.modulePaths() {
		if arg == mod || strings.HasPrefix(arg, mod+"/") {
			fullPath = true
			break
		}
	}
	if !fullPath {
		if _, ok := idx.Modules[arg]; ok {
			out := map[string]bool{}
			for pkg, info := range idx.Packages {
				if info.ModuleArea == arg {
					out[pkg] = true
				}
			}
			if len(out) > 0 {
				return out, "module", nil
			}
		}
	}
	if pkg, ok := idx.ResolvePackage(arg); ok {
		return map[string]bool{pkg: true}, "package", nil
	}
	if _, ok := idx.Modules[arg]; ok {
		out := map[string]bool{}
		for pkg, info := range idx.Packages {
			if info.ModuleArea == arg {
				out[pkg] = true
			}
		}
		if len(out) > 0 {
			return out, "module", nil
		}
	}
	return nil, "", fmt.Errorf("package or module not found: %s", arg)
}

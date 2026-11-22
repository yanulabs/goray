package index

import (
	"fmt"
	"sort"
	"strings"
)

// Resolve finds symbols by exact ID, exact name, then ID substring.
func (idx *Index) Resolve(query, kind string, limit int) []*Symbol {
	q := strings.TrimSpace(query)
	if sym, ok := idx.Symbols[q]; ok {
		return []*Symbol{sym}
	}
	if sym, ok := idx.CanonicalSymbols[q]; ok {
		return []*Symbol{sym}
	}

	q = strings.ToLower(q)
	matches := make([]*Symbol, 0)
	for _, sym := range idx.SymbolsByName[q] {
		if kind == "" || sym.Kind == kind {
			matches = append(matches, sym)
		}
	}
	if len(matches) == 0 {
		for _, sym := range idx.Symbols {
			if kind != "" && sym.Kind != kind {
				continue
			}
			if strings.Contains(strings.ToLower(sym.ID), q) {
				matches = append(matches, sym)
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		xi, xj := len(idx.XRefs[matches[i].ID]), len(idx.XRefs[matches[j].ID])
		if xi != xj {
			return xi > xj
		}
		return matches[i].ID < matches[j].ID
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// AmbiguousSymbolError asks the caller to provide one of several IDs.
type AmbiguousSymbolError struct{ IDs []string }

func (e *AmbiguousSymbolError) Error() string {
	return "ambiguous symbol; use an exact ID:\n- " + strings.Join(e.IDs, "\n- ")
}

// SingleSymbol resolves a query only when it identifies exactly one symbol.
func (idx *Index) SingleSymbol(query string) (*Symbol, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("'symbol' must not be empty")
	}
	if sym, ok := idx.Symbols[query]; ok {
		return sym, nil
	}
	if sym, ok := idx.CanonicalSymbols[query]; ok {
		return sym, nil
	}
	matches := idx.Resolve(query, "", 10)
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("symbol not found: %s", query)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return nil, &AmbiguousSymbolError{IDs: ids}
	}
}

// RelPath is the module-relative path of a package.
func (idx *Index) RelPath(pkgPath string) string {
	if pi, ok := idx.Packages[pkgPath]; ok {
		return pi.RelPath
	}
	for _, mod := range idx.modulePaths() {
		if pkgPath == mod {
			return "."
		}
		if strings.HasPrefix(pkgPath, mod+"/") {
			return strings.TrimPrefix(pkgPath, mod+"/")
		}
	}
	return pkgPath
}

func (idx *Index) modulePaths() []string {
	if len(idx.ModulePaths) > 0 {
		return idx.ModulePaths
	}
	if idx.ModulePath != "" {
		return []string{idx.ModulePath}
	}
	return nil
}

// ResolvePackage maps a relative/full package path or a module name to a
// package path. A module name resolves to its root package. In workspace mode
// the exact or relative package path is resolved against every module.
func (idx *Index) ResolvePackage(arg string) (string, bool) {
	arg = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(arg), "./"), "/")

	switch {
	case arg == "" || arg == ".":
		p := idx.ModulePath
		_, ok := idx.Packages[p]
		return p, ok
	case strings.HasPrefix(arg, idx.ModulePath):
		_, ok := idx.Packages[arg]
		return arg, ok
	}

	for _, mod := range idx.modulePaths() {
		if strings.HasPrefix(arg, mod+"/") {
			_, ok := idx.Packages[arg]
			return arg, ok
		}
		if candidate := mod + "/" + arg; idx.Packages[candidate] != nil {
			return candidate, true
		}
	}
	if idx.Modules[arg] != nil {
		return idx.moduleRoot(arg)
	}
	return "", false
}

func (idx *Index) moduleRoot(name string) (string, bool) {
	var roots []string
	for pkg, pi := range idx.Packages {
		if pi.ModuleArea == name {
			roots = append(roots, pkg)
		}
	}
	if len(roots) == 0 {
		return "", false
	}
	sort.Slice(roots, func(i, j int) bool {
		ri, rj := idx.Packages[roots[i]].RelPath, idx.Packages[roots[j]].RelPath
		if len(ri) != len(rj) {
			return len(ri) < len(rj)
		}
		return ri < rj
	})
	return roots[0], true
}

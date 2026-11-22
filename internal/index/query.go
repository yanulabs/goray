package index

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SymbolIDFormats lists the identifier shapes accepted by symbol-scoped tools.
var SymbolIDFormats = []string{
	"sym:<import path>.<Name>",
	"sym:<import path>.(*Type).Method",
	"<import path>.<Name>",
	"<package dir>.<Name>",
	"<package dir>.<Type>.<Method>",
	"<Type>.<Method>",
}

// Resolve finds symbols by exact ID, canonical ID, exact name, tolerant
// shorthand, then ID substring.
func (idx *Index) Resolve(query, kind string, limit int) []*Symbol {
	q := strings.TrimSpace(query)
	if sym, ok := idx.Symbols[q]; ok {
		return []*Symbol{sym}
	}
	if sym, ok := idx.CanonicalSymbols[q]; ok {
		return []*Symbol{sym}
	}

	matches := idx.exactNameMatches(q, kind)
	if len(matches) == 0 {
		matches = idx.shorthandMatches(q, kind)
	}
	if len(matches) > 0 {
		return sortedSymbols(idx, matches, limit)
	}

	q = strings.ToLower(q)
	matches = make([]*Symbol, 0)
	for _, sym := range idx.Symbols {
		if kind != "" && sym.Kind != kind {
			continue
		}
		if strings.Contains(strings.ToLower(sym.ID), q) {
			matches = append(matches, sym)
		}
	}
	return sortedSymbols(idx, matches, limit)
}

func sortedSymbols(idx *Index, matches []*Symbol, limit int) []*Symbol {
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

func (idx *Index) exactNameMatches(query, kind string) []*Symbol {
	q := strings.ToLower(strings.TrimSpace(query))
	matches := make([]*Symbol, 0)
	for _, sym := range idx.SymbolsByName[q] {
		if kind == "" || sym.Kind == kind {
			matches = append(matches, sym)
		}
	}
	return matches
}

// shorthandMatches accepts repo-relative package shorthands and method/field
// shorthands. Exact ID and canonical lookups happen before this runs, so this
// only fills the gaps that used to fail with "symbol not found".
func (idx *Index) shorthandMatches(query, kind string) []*Symbol {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	matches := make([]*Symbol, 0)
	for _, sym := range idx.Symbols {
		if kind != "" && sym.Kind != kind {
			continue
		}
		if symbolMatchesShorthand(sym, q) {
			matches = append(matches, sym)
		}
	}
	return matches
}

func symbolMatchesShorthand(sym *Symbol, query string) bool {
	if sym == nil {
		return false
	}
	if sym.PkgRel != "" {
		if query == sym.PkgRel+"."+symbolLeaf(sym) {
			return true
		}
	}
	if strings.HasPrefix(query, "sym:") {
		if canonicalMatchesWithoutPointer(sym, query) {
			return true
		}
	}
	if leaf := symbolLeaf(sym); leaf != "" {
		if query == leaf {
			return true
		}
		if sym.RecvType != "" && query == sym.RecvType+"."+sym.Name {
			return true
		}
	}
	if strings.HasSuffix(sym.ID, query) && len(sym.ID) > len(query) {
		switch sym.ID[len(sym.ID)-len(query)-1] {
		case '/', '.':
			return true
		}
	}
	return false
}

func symbolLeaf(sym *Symbol) string {
	if sym.RecvType != "" {
		return sym.RecvType + "." + sym.Name
	}
	return sym.Name
}

func canonicalMatchesWithoutPointer(sym *Symbol, query string) bool {
	canonical := sym.CanonicalID()
	if canonical == "" {
		return false
	}
	if canonical == query {
		return true
	}
	withoutStar := strings.ReplaceAll(canonical, "*", "")
	if withoutStar == query {
		return true
	}
	unwrapped := strings.ReplaceAll(withoutStar, "(", "")
	unwrapped = strings.ReplaceAll(unwrapped, ")", "")
	return unwrapped == query
}

// AmbiguousSymbolError asks the caller to provide one of several IDs.
type AmbiguousSymbolError struct {
	Query string
	IDs   []string
}

func (e *AmbiguousSymbolError) Error() string {
	ids := e.IDs
	if len(ids) > 5 {
		ids = ids[:5]
	}
	prefix := "ambiguous symbol; use an exact ID:"
	if e.Query != "" {
		prefix = fmt.Sprintf("ambiguous symbol %q; use an exact ID:", e.Query)
	}
	return prefix + "\n- " + strings.Join(ids, "\n- ")
}

// PackageTargetError reports that the input names a package rather than a
// symbol, so package-scoped tools can be suggested instead.
type PackageTargetError struct {
	Query   string
	Package string
}

func (e *PackageTargetError) Error() string {
	return fmt.Sprintf("input %q resolves to package %s, not a symbol; use get_api_surface or get_dependency_graph", e.Query, e.Package)
}

// SymbolNotFoundError reports an unresolved target with the accepted formats.
type SymbolNotFoundError struct {
	Query       string
	Suggestions []string
}

func (e *SymbolNotFoundError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "symbol not found: %s", e.Query)
	b.WriteString("; expected one of: ")
	b.WriteString(strings.Join(SymbolIDFormats, ", "))
	if len(e.Suggestions) > 0 {
		if len(e.Suggestions) > 5 {
			e.Suggestions = e.Suggestions[:5]
		}
		b.WriteString("; nearest matches: ")
		b.WriteString(strings.Join(e.Suggestions, ", "))
	}
	return b.String()
}

// SingleSymbol resolves a query only when it identifies exactly one symbol.
func (idx *Index) SingleSymbol(query string) (*Symbol, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("'symbol_id' must not be empty")
	}
	if sym, ok := idx.Symbols[query]; ok {
		return sym, nil
	}
	if sym, ok := idx.CanonicalSymbols[query]; ok {
		return sym, nil
	}

	matches := idx.exactNameMatches(query, "")
	if len(matches) == 0 {
		matches = idx.shorthandMatches(query, "")
	}
	if len(matches) == 0 {
		if pkg, ok := idx.ResolvePackage(query); ok {
			return nil, &PackageTargetError{Query: query, Package: pkg}
		}
		matches = idx.Resolve(query, "", 10)
	}
	switch len(matches) {
	case 0:
		suggestions := make([]string, 0, 5)
		for _, sym := range idx.Resolve(query, "", 5) {
			suggestions = append(suggestions, displaySymbolID(sym))
		}
		return nil, &SymbolNotFoundError{Query: query, Suggestions: suggestions}
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, displaySymbolID(m))
		}
		return nil, &AmbiguousSymbolError{Query: query, IDs: ids}
	}
}

func displaySymbolID(sym *Symbol) string {
	if canonical := sym.CanonicalID(); canonical != "" {
		return canonical
	}
	return sym.ID
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
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "pkg:")
	arg = strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/")
	if strings.HasPrefix(arg, "mod:") {
		return idx.moduleRoot(strings.TrimPrefix(arg, "mod:"))
	}

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

// ResolveModule maps canonical module IDs, module names, and package paths to
// an indexed module name.
func (idx *Index) ResolveModule(arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "mod:")
	if arg == "" {
		return "", false
	}
	if idx.Modules[arg] != nil {
		return arg, true
	}
	if pkg, ok := idx.ResolvePackage(arg); ok && idx.Packages[pkg] != nil {
		name := idx.Packages[pkg].ModuleArea
		if name != "" && idx.Modules[name] != nil {
			return name, true
		}
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

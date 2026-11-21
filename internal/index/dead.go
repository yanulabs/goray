package index

import "strings"

// DeadFunctions returns functions and methods with no recorded references,
// excluding entry points, standard hooks, and interface implementations.
func (idx *Index) DeadFunctions(pkgPrefix string, includeExported bool) []*Symbol {
	out := make([]*Symbol, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "func" && sym.Kind != "method" {
			continue
		}
		if pkgPrefix != "" && !strings.HasPrefix(sym.PkgRel, pkgPrefix) {
			continue
		}
		if !includeExported && sym.Exported {
			continue
		}
		if sym.Name == "main" || sym.Name == "init" || isStdHook(sym.Name) {
			continue
		}
		if idx.IfaceMethods[sym.ID] || len(idx.XRefs[sym.ID]) > 0 {
			continue
		}
		out = append(out, sym)
	}
	sortSymbols(out)
	return out
}

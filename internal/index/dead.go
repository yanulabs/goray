package index

import (
	"sort"
	"strings"
)

// DeadCodeOptions controls closed-world dead-code analysis.
type DeadCodeOptions struct {
	PkgPrefix        string
	Module           string
	ExcludeModules   []string
	IncludeExported  bool
	IncludeGenerated bool
}

func (opts DeadCodeOptions) excludes(module string) bool {
	for _, excluded := range opts.ExcludeModules {
		if excluded != "" && module == excluded {
			return true
		}
	}
	return false
}

// isClosedWorld reports packages whose exported symbols are still internal to
// the module. internal/ and cmd/ cannot be imported externally, so exported
// functions there are audited even when include_exported is false.
func isClosedWorld(pkgRel string) bool {
	return pkgRel == "internal" || strings.HasPrefix(pkgRel, "internal/") ||
		pkgRel == "cmd" || strings.HasPrefix(pkgRel, "cmd/")
}

// DeadCode returns functions and methods with no recorded references, after
// suppressing interface-satisfaction and runtime-hook false positives.
func (idx *Index) DeadCode(opts DeadCodeOptions) []*Symbol {
	out := make([]*Symbol, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "func" && sym.Kind != "method" {
			continue
		}
		if sym.IsTest {
			continue
		}
		if sym.IsHook || idx.IfaceMethods[sym.ID] || len(idx.XRefs[sym.ID]) > 0 {
			continue
		}
		if !opts.IncludeGenerated && sym.Generated {
			continue
		}
		if opts.PkgPrefix != "" && !strings.HasPrefix(sym.PkgRel, opts.PkgPrefix) {
			continue
		}
		if opts.Module != "" && sym.ModuleArea != opts.Module {
			continue
		}
		if opts.excludes(sym.ModuleArea) {
			continue
		}
		if !opts.IncludeExported && sym.Exported && !isClosedWorld(sym.PkgRel) {
			continue
		}
		out = append(out, sym)
	}
	sortSymbols(out)
	return out
}

// DeadFunctions is the legacy entry point. It preserves the old behavior of
// including generated symbols and all exported symbols.
func (idx *Index) DeadFunctions(pkgPrefix string, includeExported bool) []*Symbol {
	return idx.DeadCode(DeadCodeOptions{
		PkgPrefix:        pkgPrefix,
		IncludeExported:  includeExported,
		IncludeGenerated: true,
	})
}

// GroupSymbolsBy groups symbols by module or package. "none" and unknown
// values return a single empty-key group.
func GroupSymbolsBy(symbols []*Symbol, groupBy string) map[string][]*Symbol {
	groups := map[string][]*Symbol{}
	for _, sym := range symbols {
		key := ""
		switch groupBy {
		case "module":
			key = sym.ModuleArea
		case "package":
			key = sym.PkgRel
		}
		if key == "" {
			key = sym.ModuleArea
		}
		groups[key] = append(groups[key], sym)
	}
	for _, items := range groups {
		sort.Slice(items, func(i, j int) bool {
			if items[i].ModuleArea != items[j].ModuleArea {
				return items[i].ModuleArea < items[j].ModuleArea
			}
			return items[i].ID < items[j].ID
		})
	}
	return groups
}

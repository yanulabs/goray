package tools

import (
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

type deadEntry struct {
	ID          string `json:"id"`
	CanonicalID string `json:"canonical_id,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	PkgRel      string `json:"pkg_rel"`
	Module      string `json:"module"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Exported    bool   `json:"exported"`
	Generated   bool   `json:"generated"`
	Untracked   bool   `json:"untracked,omitempty"`
}

func newDeadEntry(sym *index.Symbol) deadEntry {
	return deadEntry{
		ID:          sym.ID,
		CanonicalID: sym.CanonicalID(),
		Name:        sym.Name,
		Kind:        sym.Kind,
		PkgRel:      sym.PkgRel,
		Module:      sym.ModuleArea,
		File:        sym.File,
		Line:        sym.Line,
		Exported:    sym.Exported,
		Generated:   sym.Generated,
		Untracked:   sym.Untracked,
	}
}

func (t *toolset) deadCode() mcp.Tool {
	return mcp.Tool{
		Name:        "get_dead_code",
		Description: "Zero-reference functions/methods with closed-world and interface-satisfaction filtering. Supports generated/exported/module filters and grouped summaries.",
		InputSchema: object(map[string]any{
			"package_prefix":    propString("Optional relative package prefix"),
			"include_exported":  propBool("Include exported symbols in public packages (default: false; internal/ and cmd/ always audited)"),
			"include_generated": propBool("Include symbols from generated files (default: false)"),
			"module":            propString("Restrict results to one module group"),
			"exclude_modules":   map[string]any{"type": "array", "items": propString("Module group to exclude"), "description": "Module groups to exclude"},
			"group_by":          propEnum("Group results: none, module, or package (default: none)", "none", "module", "package"),
			"limit":             propInt("Max results (or groups when grouped; default: 100)"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			groupBy := strArg(args, "group_by", "none")
			switch groupBy {
			case "none", "module", "package":
			default:
				return "invalid group_by: " + groupBy, true
			}
			dead := idx.DeadCode(index.DeadCodeOptions{
				PkgPrefix:        strings.TrimPrefix(firstStringArg(args, "package_prefix", "package", "pkg_path"), "./"),
				Module:           strings.TrimPrefix(moduleArg(args), "mod:"),
				ExcludeModules:   strListArg(args, "exclude_modules"),
				IncludeExported:  boolArg(args, "include_exported", false),
				IncludeGenerated: boolArg(args, "include_generated", false),
			})
			total := len(dead)
			limit := intArg(args, "limit", 100)
			out := map[string]any{
				"total_candidates": total,
				"group_by":         groupBy,
				"index_state":      t.src.Status().IndexState,
			}
			if groupBy == "none" {
				if limit > 0 && len(dead) > limit {
					dead = dead[:limit]
				}
				items := make([]deadEntry, 0, len(dead))
				for _, sym := range dead {
					items = append(items, newDeadEntry(sym))
				}
				out["candidates"] = items
				return render.JSON(out), false
			}

			groups := index.GroupSymbolsBy(dead, groupBy)
			names := make([]string, 0, len(groups))
			for name := range groups {
				names = append(names, name)
			}
			sort.Slice(names, func(i, j int) bool {
				if len(groups[names[i]]) != len(groups[names[j]]) {
					return len(groups[names[i]]) > len(groups[names[j]])
				}
				return names[i] < names[j]
			})
			if limit > 0 && len(names) > limit {
				names = names[:limit]
			}
			summaries := make([]map[string]any, 0, len(names))
			for _, name := range names {
				items := groups[name]
				top := items
				if len(top) > 10 {
					top = top[:10]
				}
				entries := make([]deadEntry, 0, len(top))
				for _, sym := range top {
					entries = append(entries, newDeadEntry(sym))
				}
				summaries = append(summaries, map[string]any{
					"group": name,
					"count": len(items),
					"top":   entries,
				})
			}
			out["groups"] = summaries
			return render.JSON(out), false
		}),
	}
}

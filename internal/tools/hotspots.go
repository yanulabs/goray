package tools

import (
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) hotspots() mcp.Tool {
	return mcp.Tool{
		Name:        "get_hotspots",
		Annotations: readOnlyAnnotations(),
		Description: "Rank functions, files, or types by risk, complexity, LOC, or fan-in with module/generated/untracked filters and static test reachability.",
		InputSchema: object(map[string]any{
			"target":            propEnum("Ranking target: func, file, or type (default: func)", "func", "file", "type"),
			"sort_by":           propEnum("Sort key: risk, complexity, loc, or fan_in (default: risk)", "risk", "complexity", "loc", "fan_in"),
			"limit":             propInt("Max items (default: 20, max: 100)"),
			"module":            propString("Restrict to one module group"),
			"exclude_modules":   map[string]any{"type": "array", "items": propString("Module group to exclude"), "description": "Module groups to exclude"},
			"min_complexity":    propInt("Minimum cyclomatic complexity"),
			"min_loc":           propInt("Minimum lines of code"),
			"include_generated": propBool("Include generated files/symbols (default: false)"),
			"tracked_only":      propBool("Exclude git-untracked files/symbols (default: false)"),
			"untested_only":     propBool("Only production functions unreachable from tests (target: func only)"),
		}),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			target := strArg(args, "target", "func")
			switch target {
			case "func", "file", "type":
			default:
				return "invalid target: " + target, true
			}
			sortBy := strArg(args, "sort_by", "risk")
			switch sortBy {
			case "risk", "complexity", "loc", "fan_in":
			default:
				return "invalid sort_by: " + sortBy, true
			}
			untestedOnly := boolArg(args, "untested_only", false)
			if untestedOnly && target != "func" {
				return "untested_only is valid only for target: func", true
			}
			limit := intArg(args, "limit", 20)
			if limit > 100 {
				limit = 100
			}
			opts := index.HotspotOptions{
				Target: target, SortBy: sortBy, Limit: limit,
				Module:           strings.TrimPrefix(moduleArg(args), "mod:"),
				ExcludeModules:   strListArg(args, "exclude_modules"),
				MinComplexity:    intArg(args, "min_complexity", 0),
				MinLoc:           intArg(args, "min_loc", 0),
				IncludeGenerated: boolArg(args, "include_generated", false),
				TrackedOnly:      boolArg(args, "tracked_only", false),
				UntestedOnly:     untestedOnly,
			}
			items, total := idx.Hotspots(opts)
			return render.JSON(map[string]any{
				"target": target, "sort_by": sortBy, "limit": limit,
				"module": opts.Module, "exclude_modules": nonNil(opts.ExcludeModules),
				"min_complexity": opts.MinComplexity, "min_loc": opts.MinLoc,
				"include_generated": opts.IncludeGenerated, "tracked_only": opts.TrackedOnly,
				"untested_only": opts.UntestedOnly,
				"total":         total, "items": nonNil(items),
				"index_state": t.src.Status().IndexState,
			}), false
		}),
	}
}

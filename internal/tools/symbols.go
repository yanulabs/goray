package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) lookupSymbol() mcp.Tool {
	return mcp.Tool{
		Name:        "lookup_symbol",
		Description: "Find symbols by canonical ID, full Go identifier, repository-relative shorthand, exact name, or fuzzy substring. Returns IDs for downstream tools and inline cached summaries when available.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Symbol ID, full Go identifier, repository-relative shorthand, name, or substring"),
			"kind":      propEnum("Optional symbol kind", "func", "method", "interface", "struct", "type", "var", "const", "field"),
			"limit":     propInt("Max results (default: 25)"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			symbols := idx.Resolve(symbolArg(args), strArg(args, "kind", ""), intArg(args, "limit", 25))
			type hit struct {
				*index.Symbol
				XRefCount        int             `json:"xref_count"`
				CanonicalID      string          `json:"canonical_id,omitempty"`
				Summary          json.RawMessage `json:"summary,omitempty"`
				SyncState        string          `json:"sync_state,omitempty"`
				DepsChanged      bool            `json:"deps_changed,omitempty"`
				EnclosingSummary json.RawMessage `json:"enclosing_summary,omitempty"`
				EnclosingID      string          `json:"enclosing_summary_id,omitempty"`
				EnclosingSync    string          `json:"enclosing_sync_state,omitempty"`
				IndexState       index.State     `json:"index_state"`
			}
			out := make([]hit, 0, len(symbols))
			for _, sym := range symbols {
				summary, state, depsChanged := t.enrich(ctx, sym)
				if sym.CanonicalID() == "" && state == "" {
					state = cache.StateNotEligible
				}
				item := hit{
					Symbol: sym, XRefCount: len(idx.XRefs[sym.ID]), CanonicalID: sym.CanonicalID(),
					Summary: summary, SyncState: string(state), DepsChanged: depsChanged,
					IndexState: t.src.Status().IndexState,
				}
				if len(summary) == 0 && t.summaries != nil {
					item.EnclosingID, item.EnclosingSummary, item.EnclosingSync = t.enclosingSummary(ctx, sym)
				}
				out = append(out, item)
			}
			return render.JSON(out), false
		}),
	}
}

// enclosingSummary returns the nearest cached package or module summary for a
// symbol that is not summary-eligible itself.
func (t *toolset) enclosingSummary(ctx context.Context, sym *index.Symbol) (string, json.RawMessage, string) {
	if t.summaries == nil || sym == nil {
		return "", nil, ""
	}
	candidates := make([]string, 0, 2)
	if sym.PkgPath != "" {
		candidates = append(candidates, "pkg:"+sym.PkgPath)
	}
	if sym.ModuleArea != "" {
		candidates = append(candidates, "mod:"+sym.ModuleArea)
	}
	for _, id := range candidates {
		summary, state, _, err := t.summaries.Enrich(ctx, id)
		if err != nil {
			continue
		}
		if len(summary) > 0 {
			return id, summary, string(state)
		}
	}
	if len(candidates) > 0 {
		_, state, _, _ := t.summaries.Enrich(ctx, candidates[0])
		return candidates[0], nil, string(state)
	}
	return "", nil, ""
}

type apiSurfaceItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Signature   string          `json:"signature"`
	LOC         string          `json:"loc"`
	XRefs       int             `json:"xrefs"`
	CanonicalID string          `json:"canonical_id,omitempty"`
	Generated   bool            `json:"generated,omitempty"`
	Summary     json.RawMessage `json:"summary,omitempty"`
	SyncState   string          `json:"sync_state,omitempty"`
	DepsChanged bool            `json:"deps_changed,omitempty"`
}

func (t *toolset) collectAPISurface(ctx context.Context, idx *index.Index, pkg string, includeGenerated bool, kindFilter string) ([]apiSurfaceItem, int) {
	items := make([]apiSurfaceItem, 0)
	generatedOmittedSymbols := 0
	for _, sym := range idx.Symbols {
		if sym.PkgPath != pkg || !sym.Exported || sym.IsTest {
			continue
		}
		if !apiKindMatches(sym.Kind, kindFilter) {
			continue
		}
		if sym.Generated && !includeGenerated {
			generatedOmittedSymbols++
			continue
		}
		name := sym.Name
		if sym.RecvType != "" {
			name = "(" + sym.RecvType + ")." + sym.Name
		}
		summary, state, depsChanged := t.enrich(ctx, sym)
		items = append(items, apiSurfaceItem{
			ID: sym.ID, Name: name, Kind: sym.Kind, Signature: sym.Signature, CanonicalID: sym.CanonicalID(),
			LOC: fmt.Sprintf("%s:%d", sym.File, sym.Line), XRefs: len(idx.XRefs[sym.ID]),
			Generated: sym.Generated, Summary: summary, SyncState: string(state), DepsChanged: depsChanged,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		ri, rj := apiKindRank(items[i].Kind), apiKindRank(items[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return items, generatedOmittedSymbols
}

func (t *toolset) apiSurface() mcp.Tool {
	return mcp.Tool{
		Name:        "get_api_surface",
		Description: "Paginated exported symbols of a package or module root, grouped by kind. Generated symbols are excluded by default and reported as generated_omitted.",
		InputSchema: object(map[string]any{
			"package":           propString("Relative package path (e.g. modules/payments) or full import path"),
			"include_generated": propBool("Include symbols from generated files (default: false)"),
			"limit":             propInt("Max symbols per page (default: 100; 0 = unlimited)"),
			"offset":            propInt("Pagination offset (default: 0)"),
			"kind":              propEnum("Optional symbol kind filter", "interface", "type", "func", "const", "var"),
		}, "package"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			arg := packageArg(args)
			pkg, ok := idx.ResolvePackage(arg)
			if !ok {
				return "package not found: " + arg, true
			}
			info := idx.Packages[pkg]
			includeGenerated := boolArg(args, "include_generated", false)
			kindFilter := strArg(args, "kind", "")
			switch kindFilter {
			case "", "interface", "type", "func", "const", "var":
			default:
				return "invalid kind: " + kindFilter, true
			}

			items, generatedOmittedSymbols := t.collectAPISurface(ctx, idx, pkg, includeGenerated, kindFilter)
			total := len(items)
			offset := nonNegativeIntArg(args, "offset", 0)
			if offset > total {
				offset = total
			}
			limit := pageSizeArg(args, "limit", 100)
			page := items[offset:]
			if limit > 0 && len(page) > limit {
				page = page[:limit]
			}
			pageTruncated := offset+len(page) < total
			generatedOmittedAny := !includeGenerated && (info.GeneratedFiles > 0 || generatedOmittedSymbols > 0 || info.GeneratedLOC > 0)
			truncated := pageTruncated || generatedOmittedAny
			grouped := map[string][]apiSurfaceItem{}
			for _, entry := range page {
				grouped[entry.Kind] = append(grouped[entry.Kind], entry)
			}
			out := map[string]any{
				"package":     pkg,
				"exported":    info.Exported,
				"kind_filter": kindFilter,
				"total_count": total,
				"offset":      offset,
				"limit":       limit,
				"returned":    len(page),
				"truncated":   truncated,
				"symbols":     grouped,
				"index_state": t.src.Status().IndexState,
			}
			hints := make([]string, 0, 2)
			if pageTruncated {
				out["next_offset"] = offset + len(page)
				hints = append(hints, fmt.Sprintf(
					`call get_api_surface with package=%q, offset=%d, limit=%d for the next page; limit=0 returns all symbols`,
					pkg, offset+len(page), limit,
				))
			}
			if !includeGenerated {
				files, symbols, loc := info.GeneratedFiles, generatedOmittedSymbols, info.GeneratedLOC
				if files > 0 || symbols > 0 || loc > 0 {
					out["generated_omitted"] = map[string]any{
						"files": files, "symbols": symbols, "loc": loc,
					}
					hints = append(hints, fmt.Sprintf(`call get_api_surface with package=%q, include_generated=true to include %d generated symbol(s)`, pkg, symbols))
				}
			}
			if len(hints) > 0 {
				out["hint"] = strings.Join(hints, "; ")
			}
			return render.JSON(out), false
		}),
	}
}

func apiKindMatches(kind, filter string) bool {
	switch filter {
	case "":
		return true
	case "func":
		return kind == "func" || kind == "method"
	case "type":
		return kind == "type" || kind == "struct"
	default:
		return kind == filter
	}
}

func apiKindRank(kind string) int {
	switch kind {
	case "interface":
		return 0
	case "type", "struct":
		return 1
	case "func", "method":
		return 2
	case "const":
		return 3
	case "var":
		return 4
	default:
		return 5
	}
}

func (t *toolset) references() mcp.Tool {
	return mcp.Tool{
		Name:        "get_references",
		Description: "All type-checked references to a symbol, including caller function and file:line. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Symbol ID, full Go identifier, or repository-relative shorthand"),
			"limit":     propInt("Max references (default: 100)"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			all := idx.XRefs[sym.ID]
			limit := intArg(args, "limit", 100)
			refs := all
			truncated := false
			if len(refs) > limit {
				refs = refs[:limit]
				truncated = true
			}
			summary, state, depsChanged := t.enrich(ctx, sym)
			return render.JSON(map[string]any{
				"symbol":       sym,
				"canonical_id": sym.CanonicalID(),
				"total":        len(all),
				"truncated":    truncated,
				"refs":         nonNil(refs),
				"summary":      summary,
				"sync_state":   state,
				"deps_changed": depsChanged,
				"index_state":  t.src.Status().IndexState,
			}), false
		}),
	}
}

func (t *toolset) callGraph() mcp.Tool {
	return mcp.Tool{
		Name:        "get_call_graph",
		Description: "BFS call graph (callers, callees, or both) from a function or method. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id":                  propString("Function/method ID, full Go identifier, or repository-relative shorthand"),
			"direction":                  propEnum("Default: both", "callers", "callees", "both"),
			"depth":                      propInt("Max depth (default: 2)"),
			"limit":                      propInt("Max nodes (default: 80)"),
			"include_interface_dispatch": propBool("Include in-module interface dispatch edges (default: true)"),
		}, "symbol_id"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			graph := idx.CallGraphWithOptions(
				sym.ID,
				strArg(args, "direction", "both"),
				intArg(args, "depth", 2),
				intArg(args, "limit", 80),
				boolArg(args, "include_interface_dispatch", true),
			)
			out := struct {
				index.Graph
				IndexState index.State `json:"index_state"`
			}{Graph: graph, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		}),
	}
}

func (t *toolset) implementations() mcp.Tool {
	return mcp.Tool{
		Name:        "find_implementations",
		Description: "Compiler-exact go/types.Implements lookup: interface to types, or type to interfaces. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Interface or concrete type ID, full Go identifier, or repository-relative shorthand"),
		}, "symbol_id"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			if sym.Kind == "interface" {
				return render.JSON(map[string]any{
					"interface":       sym,
					"implementations": nonNil(idx.IfaceImpls[sym.ID]),
					"index_state":     t.src.Status().IndexState,
				}), false
			}
			return render.JSON(map[string]any{
				"type":        sym,
				"interfaces":  nonNil(idx.TypeImpls[sym.ID]),
				"index_state": t.src.Status().IndexState,
			}), false
		}),
	}
}

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

func (t *toolset) functionBody() mcp.Tool {
	return mcp.Tool{
		Name:        "get_function_body",
		Description: "Exact source, cyclomatic complexity, and LOC of a function or method. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Function/method ID, full Go identifier, or repository-relative shorthand"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			if sym.EndLine < sym.Line {
				return "symbol has no source body: " + sym.ID, true
			}
			raw, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.File)))
			if err != nil {
				return err.Error(), true
			}
			lines := strings.Split(string(raw), "\n")
			start := max(sym.Line-1, 0)
			end := min(sym.EndLine, len(lines))
			if start >= end {
				return "source range is stale; reindex and retry", true
			}

			var b strings.Builder
			if state := t.src.Status().IndexState; state != index.StateReady {
				fmt.Fprintf(&b, "// index_state: %s\n", state)
			}
			if canonical := sym.CanonicalID(); canonical != "" {
				fmt.Fprintf(&b, "// canonical_id: %s\n", canonical)
			}
			if summary, state, depsChanged := t.enrich(ctx, sym); len(summary) > 0 {
				fmt.Fprintf(&b, "// sync_state: %s deps_changed=%v\n// summary: %s\n", state, depsChanged, summary)
			}
			fmt.Fprintf(&b, "// %s (%s:%d-%d | LOC=%d | cyclomatic=%d)\n",
				sym.ID, sym.File, sym.Line, sym.EndLine, sym.LOC, sym.Cyclomatic)
			for i := start; i < end; i++ {
				fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
			}
			return b.String(), false
		}),
	}
}

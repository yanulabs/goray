package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

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

type apiSurfacePage struct {
	Items      []apiSurfaceItem
	Offset     int
	Limit      int
	NextOffset int
	HasNext    bool
}

func paginateAPISurface(items []apiSurfaceItem, offset, limit int) apiSurfacePage {
	offset = max(offset, 0)
	if offset > len(items) {
		offset = len(items)
	}
	page := items[offset:]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	out := apiSurfacePage{Items: page, Offset: offset, Limit: limit}
	if offset+len(page) < len(items) {
		out.NextOffset = offset + len(page)
		out.HasNext = true
	}
	return out
}

type apiSurfaceResponse struct {
	Package          string
	Info             *index.PkgInfo
	KindFilter       string
	IncludeGenerated bool
	Total            int
	GeneratedOmitted int
	Page             apiSurfacePage
	IndexState       index.State
}

func buildAPISurfaceResponse(response apiSurfaceResponse) map[string]any {
	page := response.Page
	generatedOmittedAny := !response.IncludeGenerated && (response.Info.GeneratedFiles > 0 || response.GeneratedOmitted > 0 || response.Info.GeneratedLOC > 0)
	grouped := map[string][]apiSurfaceItem{}
	for _, entry := range page.Items {
		grouped[entry.Kind] = append(grouped[entry.Kind], entry)
	}
	out := map[string]any{
		"package":     response.Package,
		"exported":    response.Info.Exported,
		"kind_filter": response.KindFilter,
		"total_count": response.Total,
		"offset":      page.Offset,
		"limit":       page.Limit,
		"returned":    len(page.Items),
		"truncated":   page.HasNext || generatedOmittedAny,
		"symbols":     grouped,
		"index_state": response.IndexState,
	}
	addAPISurfaceHints(out, response.Package, response.IncludeGenerated, page, response.Info, response.GeneratedOmitted)
	return out
}

func addAPISurfaceHints(out map[string]any, pkg string, includeGenerated bool, page apiSurfacePage, info *index.PkgInfo, generatedOmittedSymbols int) {
	hints := make([]string, 0, 2)
	if page.HasNext {
		out["next_offset"] = page.NextOffset
		hints = append(hints, fmt.Sprintf(
			`call get_api_surface with package=%q, offset=%d, limit=%d for the next page; limit=0 returns all symbols`,
			pkg, page.NextOffset, page.Limit,
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
			page := paginateAPISurface(
				items,
				nonNegativeIntArg(args, "offset", 0),
				pageSizeArg(args, "limit", 100),
			)
			out := buildAPISurfaceResponse(apiSurfaceResponse{
				Package: pkg, Info: info, KindFilter: kindFilter, IncludeGenerated: includeGenerated,
				Total: len(items), GeneratedOmitted: generatedOmittedSymbols, Page: page,
				IndexState: t.src.Status().IndexState,
			})
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

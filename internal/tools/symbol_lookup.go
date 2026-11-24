package tools

import (
	"context"
	"encoding/json"

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

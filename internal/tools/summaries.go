package tools

import (
	"context"
	"encoding/json"

	"github.com/yanulabs/go-mcp-server/internal/cache"
	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

func (t *toolset) enrich(ctx context.Context, sym *index.Symbol) (json.RawMessage, cache.SyncState, bool) {
	if t.summaries == nil || sym == nil {
		return nil, "", false
	}
	canonical := sym.CanonicalID()
	if canonical == "" {
		return nil, "", false
	}
	summary, state, depsChanged, err := t.summaries.Enrich(ctx, canonical)
	if err != nil {
		return nil, "", false
	}
	return summary, state, depsChanged
}

func (t *toolset) listStaleSummaries() mcp.Tool {
	return mcp.Tool{
		Name:        "list_stale_summaries",
		Description: "List eligible symbols that need summaries, ordered bottom-up and excluding synced/not-eligible symbols.",
		InputSchema: object(map[string]any{
			"kind":         propEnum("Optional symbol kind", "func", "method", "type", "file", "package", "module"),
			"module":       propString("Optional module name"),
			"pkg_path":     propString("Optional exact package path"),
			"state":        propEnum("Optional sync state", "missing", "hard_stale", "soft_stale"),
			"limit":        propInt("Max results (default 50, max 200)"),
			"include_dead": propBool("Include dead functions/methods (default false)"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			if t.summaries == nil {
				return "summary cache is unavailable", true
			}
			filter := cache.StaleFilter{
				Kind:        strArg(args, "kind", ""),
				Module:      strArg(args, "module", ""),
				PkgPath:     strArg(args, "pkg_path", ""),
				State:       cache.SyncState(strArg(args, "state", "")),
				Limit:       intArg(args, "limit", 50),
				IncludeDead: boolArg(args, "include_dead", false),
			}
			items, err := t.summaries.ListStale(ctx, filter)
			if err != nil {
				return err.Error(), true
			}
			return render.JSON(map[string]any{
				"index_state":    t.src.Status().IndexState,
				"prompt_version": cache.PromptVersion,
				"items":          nonNil(items),
			}), false
		},
	}
}

func (t *toolset) getSummaryContext() mcp.Tool {
	return mcp.Tool{
		Name:        "get_summary_context",
		Description: "One-round-trip context for producing or updating a structured summary: schema, hashes, staleness, source, and dependencies.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Canonical symbol ID (e.g. sym:pkg.(*Svc).Run)"),
		}, "symbol_id"),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			if t.summaries == nil {
				return "summary cache is unavailable", true
			}
			id := strArg(args, "symbol_id", "")
			if id == "" {
				return "'symbol_id' must not be empty", true
			}
			idx, err := t.src.Wait(ctx)
			if err != nil {
				return err.Error(), true
			}
			summaryContext, err := t.summaries.SummaryContext(ctx, id, idx)
			if err != nil {
				return err.Error(), true
			}
			out := struct {
				cache.SummaryContext
				IndexState index.State `json:"index_state"`
			}{SummaryContext: summaryContext, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		},
	}
}

func (t *toolset) upsertSummaries() mcp.Tool {
	return mcp.Tool{
		Name:        "upsert_summaries",
		Description: "Validate and store client-generated structured summaries with optimistic hash checks.",
		Exclusive:   true,
		InputSchema: object(map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"symbol_id", "summary", "expected_self_hash", "expected_dep_hash"},
					"properties": map[string]any{
						"symbol_id":          propString("Canonical symbol ID"),
						"summary":            map[string]any{"type": "object", "description": "Kind-specific structured summary"},
						"expected_self_hash": propString("self_hash returned by get_summary_context"),
						"expected_dep_hash":  propString("dep_hash returned by get_summary_context"),
						"model_id":           propString("Optional model identifier"),
					},
				},
			},
			"model_id": propString("Default model identifier for items"),
		}, "items"),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			if t.summaries == nil {
				return "summary cache is unavailable", true
			}
			var payload struct {
				Items   []cache.SummaryInput `json:"items"`
				ModelID string               `json:"model_id"`
			}
			data, err := json.Marshal(args)
			if err != nil {
				return err.Error(), true
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				return "invalid items: " + err.Error(), true
			}
			if len(payload.Items) == 0 {
				return "'items' must not be empty", true
			}
			for i := range payload.Items {
				if payload.Items[i].ModelID == "" {
					payload.Items[i].ModelID = payload.ModelID
				}
			}
			results, err := t.summaries.UpsertSummaries(ctx, payload.Items)
			if err != nil {
				return err.Error(), true
			}
			return render.JSON(map[string]any{
				"index_state":    t.src.Status().IndexState,
				"prompt_version": cache.PromptVersion,
				"results":        nonNil(results),
			}), false
		},
	}
}

func (t *toolset) getSummaryCoverage() mcp.Tool {
	return mcp.Tool{
		Name:        "get_summary_coverage",
		Description: "Summary coverage counts grouped by kind and module.",
		InputSchema: object(map[string]any{
			"module": propString("Optional module name"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			if t.summaries == nil {
				return "summary cache is unavailable", true
			}
			coverage, err := t.summaries.Coverage(ctx, strArg(args, "module", ""))
			if err != nil {
				return err.Error(), true
			}
			out := struct {
				cache.Coverage
				IndexState index.State `json:"index_state"`
			}{Coverage: coverage, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		},
	}
}

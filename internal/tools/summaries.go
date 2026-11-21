package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yanulabs/go-mcp-server/internal/cache"
	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/mcp"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

func stringSliceArg(args map[string]any, key string) ([]string, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", key, i)
		}
		out = append(out, text)
	}
	return out, nil
}

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
		Description: "Single or batched context for producing/updating summaries: schema, hashes, staleness, source, and dependencies (max 25, budgeted).",
		InputSchema: object(map[string]any{
			"symbol_id":  propString("Canonical symbol ID (single-symbol mode)"),
			"symbol_ids": map[string]any{"type": "array", "maxItems": 25, "items": map[string]any{"type": "string"}, "description": "Batch canonical symbol IDs (max 25)"},
			"max_chars":  propInt("Batch output budget in characters (default 40000)"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			if t.summaries == nil {
				return "summary cache is unavailable", true
			}
			idx, err := t.src.Wait(ctx)
			if err != nil {
				return err.Error(), true
			}
			id := strArg(args, "symbol_id", "")
			ids, err := stringSliceArg(args, "symbol_ids")
			if err != nil {
				return err.Error(), true
			}
			if id == "" && len(ids) == 0 {
				return "provide 'symbol_id' or 'symbol_ids'", true
			}
			if id != "" {
				summaryContext, err := t.summaries.SummaryContext(ctx, id, idx)
				if err != nil {
					return err.Error(), true
				}
				out := struct {
					cache.SummaryContext
					IndexState index.State `json:"index_state"`
				}{SummaryContext: summaryContext, IndexState: t.src.Status().IndexState}
				return render.JSON(out), false
			}
			if len(ids) > 25 {
				return "symbol_ids exceeds the maximum of 25", true
			}
			maxChars := intArg(args, "max_chars", 40000)
			contexts := make([]json.RawMessage, 0, len(ids))
			truncated := make([]string, 0)
			used := 0
			for i, symbolID := range ids {
				summaryContext, err := t.summaries.SummaryContext(ctx, symbolID, idx)
				if err != nil {
					return err.Error(), true
				}
				raw := json.RawMessage(render.JSON(summaryContext))
				if len(contexts) > 0 && used+len(raw) > maxChars {
					truncated = append(truncated, ids[i:]...)
					break
				}
				contexts = append(contexts, raw)
				used += len(raw)
			}
			out := struct {
				PromptVersion string            `json:"prompt_version"`
				IndexState    index.State       `json:"index_state"`
				Contexts      []json.RawMessage `json:"contexts"`
				TruncatedIDs  []string          `json:"truncated_ids"`
			}{
				PromptVersion: cache.PromptVersion,
				IndexState:    t.src.Status().IndexState,
				Contexts:      contexts,
				TruncatedIDs:  truncated,
			}
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

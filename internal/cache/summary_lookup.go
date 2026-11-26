package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Cached is the report-facing view of one symbol's summary state.
type Cached struct {
	Summary     json.RawMessage
	State       SyncState
	DepsChanged bool
	ModelID     string
}

// Lookup returns the cached summary and sync state for a canonical symbol ID.
func (c *Cache) Lookup(ctx context.Context, id string) (Cached, error) {
	sym, err := c.workingSymbol(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Cached{State: StateMissing}, nil
	}
	if err != nil {
		return Cached{}, err
	}
	state := c.classify(ctx, sym)
	out := Cached{State: state, DepsChanged: state == StateSoftStale}
	if state == StateNotEligible || state == StateMissing {
		return out, nil
	}
	summary, hash, err := c.cachedForState(ctx, sym, state)
	if err != nil {
		return Cached{}, err
	}
	out.Summary = summary
	if hash != "" {
		c.touch(hash)
		if err := c.queryRowContext(ctx,
			"SELECT model_id FROM "+c.summaryTable("summary_cache")+" WHERE content_hash = ? AND prompt_version = ?",
			hash, PromptVersion).Scan(&out.ModelID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Cached{}, err
		}
	}
	return out, nil
}

// LookupAll returns the report-facing summary state for every working symbol
// in one joined query. Report assembly previously issued up to four statements
// per symbol, which dominated export time on large repositories.
func (c *Cache) LookupAll(ctx context.Context) (map[string]Cached, error) {
	stateExpr := c.stateExpr()
	summaryHashExpr := `CASE w.state
    WHEN 'synced' THEN w.content_hash
    WHEN 'soft_stale' THEN COALESCE(
        (SELECT h.last_content_hash FROM ` + c.summaryTable("symbol_history") + ` h
         JOIN ` + c.summaryTable("summary_cache") + ` c2 ON c2.content_hash = h.last_content_hash AND c2.prompt_version = h.prompt_version
         WHERE h.symbol_id = w.id AND h.prompt_version = ? AND c2.self_hash = w.self_hash),
        (SELECT c3.content_hash FROM ` + c.summaryTable("summary_cache") + ` c3
         WHERE c3.prompt_version = ? AND c3.self_hash = w.self_hash AND c3.kind = w.kind
           AND w.self_hash <> '' AND w.kind IN ('func','method','type')
         ORDER BY c3.created_at DESC LIMIT 1)
    )
    WHEN 'hard_stale' THEN (
        SELECT h.last_content_hash FROM ` + c.summaryTable("symbol_history") + ` h
        WHERE h.symbol_id = w.id AND h.prompt_version = ?
    )
    ELSE NULL END`
	query := `SELECT x.id, x.state, COALESCE(sc.content_hash, ''), COALESCE(sc.summary_json, ''), COALESCE(sc.model_id, '')
FROM (
    SELECT w.*, ` + summaryHashExpr + ` AS matched_hash
    FROM (
        SELECT w.id, ` + stateExpr + ` AS state, w.content_hash, w.self_hash, w.kind
        FROM working_symbols w
    ) w
) x
LEFT JOIN ` + c.summaryTable("summary_cache") + ` sc ON sc.content_hash = x.matched_hash AND sc.prompt_version = ?`
	args := []any{
		PromptVersion, PromptVersion, PromptVersion, PromptVersion,
		PromptVersion, PromptVersion, PromptVersion, PromptVersion,
	}
	rows, err := c.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]Cached{}
	for rows.Next() {
		var id, state, hash, summary, modelID string
		if err := rows.Scan(&id, &state, &hash, &summary, &modelID); err != nil {
			return nil, err
		}
		item := Cached{State: SyncState(state), DepsChanged: state == string(StateSoftStale), ModelID: modelID}
		if summary != "" {
			item.Summary = json.RawMessage(summary)
		}
		if hash != "" {
			c.touch(hash)
		}
		out[id] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

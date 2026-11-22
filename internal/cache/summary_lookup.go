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
		if err := c.db.QueryRowContext(ctx,
			"SELECT model_id FROM "+c.summaryTable("summary_cache")+" WHERE content_hash = ? AND prompt_version = ?",
			hash, PromptVersion).Scan(&out.ModelID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Cached{}, err
		}
	}
	return out, nil
}

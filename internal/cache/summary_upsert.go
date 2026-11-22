package cache

import (
	"context"
	"database/sql"
	"errors"
)

// UpsertSummaries validates and stores client-produced summaries. Reads of
// working_symbols happen before the write transaction so the transaction only
// ever modifies summary tables.
func (c *Cache) UpsertSummaries(ctx context.Context, items []SummaryInput) ([]UpsertResult, error) {
	type preparedSummary struct {
		index   int
		sym     *WorkingSymbol
		raw     []byte
		modelID string
	}
	results := make([]UpsertResult, len(items))
	prepared := make([]preparedSummary, 0, len(items))

	for i, item := range items {
		results[i] = UpsertResult{SymbolID: item.SymbolID, Status: "validation_error"}
		sym, err := c.workingSymbol(ctx, item.SymbolID)
		if errors.Is(err, sql.ErrNoRows) {
			results[i].Status = "not_found"
			results[i].Error = "symbol not found in current working tree"
			continue
		}
		if err != nil {
			return nil, err
		}
		if !sym.Eligible {
			results[i].Error = "symbol is not eligible for summarization"
			continue
		}
		if err := validateSummary(sym.Kind, item.Summary); err != nil {
			results[i].Error = err.Error()
			continue
		}
		if sym.SelfHash != item.ExpectedSelfHash || sym.DepHash != item.ExpectedDepHash {
			results[i].Status = "hash_mismatch"
			results[i].Error = "expected hashes do not match current working tree; fetch context again"
			continue
		}
		raw, err := compactJSON(item.Summary)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		prepared = append(prepared, preparedSummary{index: i, sym: sym, raw: raw, modelID: item.ModelID})
	}
	if len(prepared) == 0 {
		return results, nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, item := range prepared {
		now := nowString()
		res, err := tx.ExecContext(ctx, `
INSERT INTO `+c.summaryTable("summary_cache")+` (content_hash, prompt_version, self_hash, dep_hash, kind, model_id, summary_json, created_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(content_hash, prompt_version) DO UPDATE SET
    self_hash = excluded.self_hash,
    dep_hash = excluded.dep_hash,
    kind = excluded.kind,
    model_id = excluded.model_id,
    summary_json = excluded.summary_json,
    created_at = excluded.created_at,
    last_accessed_at = excluded.last_accessed_at`,
			item.sym.ContentHash, PromptVersion, item.sym.SelfHash, item.sym.DepHash, item.sym.Kind, item.modelID, string(item.raw), now, now)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			results[item.index].Status = "validation_error"
			results[item.index].Error = "summary cache write did not replace a row; retry after reindexing"
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO `+c.summaryTable("symbol_history")+` (symbol_id, prompt_version, last_content_hash, last_self_hash, last_dep_hash, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol_id, prompt_version) DO UPDATE SET
    last_content_hash = excluded.last_content_hash,
    last_self_hash = excluded.last_self_hash,
    last_dep_hash = excluded.last_dep_hash,
    updated_at = excluded.updated_at`,
			item.sym.ID, PromptVersion, item.sym.ContentHash, item.sym.SelfHash, item.sym.DepHash, now); err != nil {
			return nil, err
		}
		results[item.index].Status = "updated"
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, item := range prepared {
		c.forgetTouch(item.sym.ContentHash)
	}
	c.maybeGC(ctx)
	return results, nil
}

// forgetTouch drops a pending LRU timestamp for a key that was just written so
// a later flush cannot regress the freshly stored last_accessed_at value.
func (c *Cache) forgetTouch(contentHash string) {
	if contentHash == "" {
		return
	}
	c.touchMu.Lock()
	delete(c.touches, touchKey{contentHash: contentHash, promptVersion: PromptVersion})
	c.touchMu.Unlock()
}

package cache

import (
	"context"
	"time"
)

// RunGC evicts unreferenced cache rows and prunes orphaned history pointers.
func (c *Cache) RunGC(ctx context.Context, now time.Time, ttl time.Duration, capacity int) error {
	c.gcMu.Lock()
	defer c.gcMu.Unlock()
	c.lastGC = now
	_ = c.FlushTouches(ctx)

	rows, err := c.db.QueryContext(ctx, `
SELECT c.content_hash, c.prompt_version, c.last_accessed_at
FROM summary_cache c
WHERE NOT EXISTS (SELECT 1 FROM working_symbols w WHERE w.content_hash = c.content_hash)
ORDER BY c.last_accessed_at ASC`)
	if err != nil {
		return err
	}
	type entry struct {
		contentHash   string
		promptVersion string
		accessed      time.Time
	}
	entries := make([]entry, 0)
	for rows.Next() {
		var e entry
		var accessed string
		if err := rows.Scan(&e.contentHash, &e.promptVersion, &accessed); err != nil {
			_ = rows.Close()
			return err
		}
		e.accessed, _ = time.Parse(time.RFC3339Nano, accessed)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	if capacity < 0 {
		capacity = 0
	}
	overCap := len(entries) - capacity
	cutoff := now.Add(-ttl)
	toDelete := make([]entry, 0)
	for i, e := range entries {
		if i < overCap || e.accessed.Before(cutoff) {
			toDelete = append(toDelete, e)
		}
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM summary_cache WHERE content_hash = ? AND prompt_version = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range toDelete {
		if _, err := stmt.ExecContext(ctx, e.contentHash, e.promptVersion); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	if _, err := tx.ExecContext(ctx, `
DELETE FROM symbol_history
WHERE NOT EXISTS (
    SELECT 1 FROM summary_cache c
    WHERE c.content_hash = symbol_history.last_content_hash
      AND c.prompt_version = symbol_history.prompt_version
)`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, _ = c.db.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)")
	_, _ = c.db.ExecContext(ctx, "PRAGMA optimize")
	return nil
}

func (c *Cache) maybeGC(ctx context.Context) {
	c.gcMu.Lock()
	due := c.lastGC.IsZero() || time.Since(c.lastGC) >= gcInterval
	c.gcMu.Unlock()
	if due {
		_ = c.RunGC(ctx, time.Now().UTC(), gcTTL, gcCapacity)
	}
}

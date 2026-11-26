package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

// Enrich returns a cached summary for existing read tools.
func (c *Cache) Enrich(ctx context.Context, id string) (json.RawMessage, SyncState, bool, error) {
	sym, err := c.workingSymbol(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, StateMissing, false, nil
		}
		return nil, StateMissing, false, err
	}
	state := c.classify(ctx, sym)
	if state == StateNotEligible {
		return nil, state, false, nil
	}
	cached, hash, err := c.cachedForState(ctx, sym, state)
	if err != nil {
		return nil, state, false, err
	}
	if hash != "" {
		c.touch(hash)
	}
	return cached, state, state == StateSoftStale, nil
}

func (c *Cache) staleReason(ctx context.Context, sym *WorkingSymbol, state SyncState) (string, bool) {
	var lastSelf, lastDep string
	err := c.queryRowContext(ctx,
		"SELECT last_self_hash, last_dep_hash FROM "+c.summaryTable("symbol_history")+" WHERE symbol_id = ? AND prompt_version = ?",
		sym.ID, PromptVersion).Scan(&lastSelf, &lastDep)
	if err != nil {
		if state == StateSoftStale {
			return "deps_changed", true
		}
		return "self_changed", false
	}
	selfChanged := lastSelf != sym.SelfHash
	depsChanged := lastDep != sym.DepHash
	switch {
	case selfChanged && depsChanged:
		return "self_changed", true
	case selfChanged:
		return "self_changed", false
	case depsChanged:
		return "deps_changed", true
	default:
		return "unknown", false
	}
}

func (c *Cache) dependencies(ctx context.Context, sym *WorkingSymbol, idx *index.Index, needSummaries bool) ([]Dependency, error) {
	ids := make([]string, 0)
	if sym.Kind == "type" {
		name := strings.TrimPrefix(sym.ID, "sym:"+sym.PkgPath+".")
		prefixes := []string{
			"sym:" + sym.PkgPath + ".(" + name + ").",
			"sym:" + sym.PkgPath + ".(*" + name + ").",
		}
		rows, err := c.queryContext(ctx,
			"SELECT id FROM working_symbols WHERE kind = 'method' AND (instr(id, ?) = 1 OR instr(id, ?) = 1) ORDER BY id",
			prefixes[0], prefixes[1])
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	} else {
		rows, err := c.queryContext(ctx,
			"SELECT dst_id FROM working_edges WHERE src_id = ? AND kind = 'calls' ORDER BY dst_id", sym.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return c.loadDependencies(ctx, ids, idx, needSummaries)
}

func (c *Cache) children(ctx context.Context, sym *WorkingSymbol, idx *index.Index, needSummaries bool) ([]Dependency, error) {
	rows, err := c.queryContext(ctx,
		"SELECT id FROM working_symbols WHERE parent_id = ? ORDER BY kind, id", sym.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	return c.loadDependencies(ctx, ids, idx, needSummaries)
}

func (c *Cache) loadDependencies(ctx context.Context, ids []string, idx *index.Index, needSummaries bool) ([]Dependency, error) {
	out := make([]Dependency, 0, len(ids))
	for _, id := range ids {
		sym, err := c.workingSymbol(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		state := c.classify(ctx, sym)
		dep := Dependency{
			ID: id, Kind: sym.Kind, SyncState: state,
			Exported: sym.Exported, Eligible: sym.Eligible,
		}
		if needSummaries {
			cached, hash, err := c.cachedForState(ctx, sym, state)
			if err != nil {
				return nil, err
			}
			if hash != "" {
				c.touch(hash)
			}
			dep.Summary = cached
		}
		if !sym.Eligible {
			dep.Source = readSource(idx, sym)
		}
		out = append(out, dep)
	}
	return out, nil
}

func (c *Cache) workingSymbol(ctx context.Context, id string) (*WorkingSymbol, error) {
	var sym WorkingSymbol
	var exported, dead, eligible int
	err := c.queryRowContext(ctx, `
SELECT id, parent_id, kind, module_name, pkg_path, file_path, start_line, end_line,
       loc, complexity, fan_in, is_exported, is_dead, is_eligible,
       sig_hash, self_hash, dep_hash, content_hash
FROM working_symbols WHERE id = ?`, id).Scan(
		&sym.ID, &sym.ParentID, &sym.Kind, &sym.ModuleName, &sym.PkgPath, &sym.FilePath,
		&sym.StartLine, &sym.EndLine, &sym.LOC, &sym.Complexity, &sym.FanIn,
		&exported, &dead, &eligible, &sym.SigHash, &sym.SelfHash, &sym.DepHash, &sym.ContentHash)
	if err != nil {
		return nil, err
	}
	sym.Exported = exported == 1
	sym.Dead = dead == 1
	sym.Eligible = eligible == 1
	return &sym, nil
}

func (c *Cache) classify(ctx context.Context, sym *WorkingSymbol) SyncState {
	if !sym.Eligible {
		return StateNotEligible
	}
	var state string
	err := c.queryRowContext(ctx, "SELECT "+c.stateExpr()+" FROM working_symbols w WHERE w.id = ?",
		PromptVersion, PromptVersion, PromptVersion, PromptVersion, sym.ID).Scan(&state)
	if err != nil {
		return StateMissing
	}
	return SyncState(state)
}

func (c *Cache) cachedForState(ctx context.Context, sym *WorkingSymbol, state SyncState) (json.RawMessage, string, error) {
	switch state {
	case StateSynced:
		return c.querySummary(ctx,
			"WHERE content_hash = ? AND prompt_version = ?", sym.ContentHash, PromptVersion)
	case StateSoftStale:
		raw, hash, err := c.querySummary(ctx,
			`WHERE prompt_version = ? AND content_hash = (
                SELECT h.last_content_hash FROM `+c.summaryTable("symbol_history")+` h
                JOIN `+c.summaryTable("summary_cache")+` c2 ON c2.content_hash = h.last_content_hash AND c2.prompt_version = h.prompt_version
                WHERE h.symbol_id = ? AND h.prompt_version = ? AND c2.self_hash = ?
            ) ORDER BY created_at DESC LIMIT 1`,
			PromptVersion, sym.ID, PromptVersion, sym.SelfHash)
		if err != nil || raw != nil {
			return raw, hash, err
		}
		if sym.SelfHash != "" && (sym.Kind == "func" || sym.Kind == "method" || sym.Kind == "type") {
			return c.querySummary(ctx,
				"WHERE self_hash = ? AND prompt_version = ? AND kind = ? ORDER BY created_at DESC LIMIT 1",
				sym.SelfHash, PromptVersion, sym.Kind)
		}
		return nil, "", nil
	case StateHardStale:
		return c.querySummary(ctx,
			`WHERE prompt_version = ? AND content_hash = (
                SELECT last_content_hash FROM `+c.summaryTable("symbol_history")+` WHERE symbol_id = ? AND prompt_version = ?
            ) ORDER BY created_at DESC LIMIT 1`,
			PromptVersion, sym.ID, PromptVersion)
	case StateMissing, StateNotEligible:
		return nil, "", nil
	default:
		return nil, "", nil
	}
}

func (c *Cache) querySummary(ctx context.Context, where string, args ...any) (json.RawMessage, string, error) {
	var hash, raw string
	err := c.queryRowContext(ctx,
		"SELECT content_hash, summary_json FROM "+c.summaryTable("summary_cache")+" "+where, args...).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return json.RawMessage(raw), hash, nil
}

// UpsertSummaries validates and stores client-produced summaries. Reads of
// working_symbols happen before the write transaction so the transaction only
// ever modifies summary tables.

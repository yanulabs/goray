package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
)

// StateOf classifies one working symbol.
func (c *Cache) StateOf(ctx context.Context, id string) (SyncState, error) {
	var state string
	err := c.db.QueryRowContext(ctx, "SELECT "+c.stateExpr()+" FROM working_symbols w WHERE w.id = ?",
		PromptVersion, PromptVersion, PromptVersion, PromptVersion, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("symbol not found: %s", id)
	}
	return SyncState(state), err
}

// ListStale returns eligible, non-synced symbols for summary work.
func (c *Cache) ListStale(ctx context.Context, filter StaleFilter) ([]StaleSymbol, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := "SELECT id, kind, module_name, pkg_path, file_path, state, self_hash, dep_hash, content_hash, loc, complexity, fan_in FROM (" +
		"SELECT w.id, w.kind, w.module_name, w.pkg_path, w.file_path, w.self_hash, w.dep_hash, w.content_hash, w.loc, w.complexity, w.fan_in, " +
		c.stateExpr() + " AS state FROM working_symbols w WHERE w.is_eligible = 1"
	args := []any{PromptVersion, PromptVersion, PromptVersion, PromptVersion}
	if filter.Kind != "" {
		query += " AND w.kind = ?"
		args = append(args, filter.Kind)
	}
	if filter.Module != "" {
		query += " AND w.module_name = ?"
		args = append(args, filter.Module)
	}
	if filter.PkgPath != "" {
		query += " AND w.pkg_path = ?"
		args = append(args, filter.PkgPath)
	}
	if !filter.IncludeDead {
		query += " AND w.is_dead = 0"
	}
	query += ") WHERE state <> 'synced'"
	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, string(filter.State))
	}
	query += ` ORDER BY
		CASE kind WHEN 'method' THEN 0 WHEN 'func' THEN 0 WHEN 'type' THEN 1 WHEN 'file' THEN 2 WHEN 'package' THEN 3 WHEN 'module' THEN 4 ELSE 5 END,
		CASE state WHEN 'hard_stale' THEN 0 WHEN 'missing' THEN 0 WHEN 'soft_stale' THEN 1 ELSE 2 END,
		pkg_path, id
	LIMIT ?`
	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]StaleSymbol, 0)
	for rows.Next() {
		var item StaleSymbol
		if err := rows.Scan(&item.ID, &item.Kind, &item.ModuleName, &item.PkgPath, &item.FilePath,
			&item.SyncState, &item.SelfHash, &item.DepHash, &item.ContentHash,
			&item.LOC, &item.Complexity, &item.FanIn); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Coverage aggregates sync states by module and kind.
func (c *Cache) Coverage(ctx context.Context, module string) (Coverage, error) {
	query := "SELECT w.kind, w.module_name, w.is_eligible, " + c.stateExpr() + " AS state FROM working_symbols w"
	args := []any{PromptVersion, PromptVersion, PromptVersion, PromptVersion}
	if module != "" {
		query += " WHERE w.module_name = ?"
		args = append(args, module)
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Coverage{}, err
	}
	defer func() { _ = rows.Close() }()

	groups := map[string]*CoverageGroup{}
	var totals CoverageCounts
	for rows.Next() {
		var kind, moduleName, state string
		var eligible int
		if err := rows.Scan(&kind, &moduleName, &eligible, &state); err != nil {
			return Coverage{}, err
		}
		key := moduleName + "\x00" + kind
		group := groups[key]
		if group == nil {
			group = &CoverageGroup{Module: moduleName, Kind: kind}
			groups[key] = group
		}
		incCounts(&group.CoverageCounts, eligible == 1, SyncState(state))
		incCounts(&totals, eligible == 1, SyncState(state))
	}
	if err := rows.Err(); err != nil {
		return Coverage{}, err
	}

	out := make([]CoverageGroup, 0, len(groups))
	for _, group := range groups {
		finalizeCoverage(&group.CoverageCounts)
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return kindRank(out[i].Kind) < kindRank(out[j].Kind)
	})
	finalizeCoverage(&totals)
	return Coverage{PromptVersion: PromptVersion, Groups: out, Totals: totals}, nil
}

func incCounts(counts *CoverageCounts, eligible bool, state SyncState) {
	counts.Total++
	if !eligible {
		counts.NotEligible++
		return
	}
	counts.Eligible++
	switch state {
	case StateSynced:
		counts.Synced++
	case StateSoftStale:
		counts.SoftStale++
	case StateHardStale:
		counts.HardStale++
	case StateMissing:
		counts.Missing++
	case StateNotEligible:
		// Handled by the !eligible branch above.
	}
}

func finalizeCoverage(counts *CoverageCounts) {
	if counts.Eligible > 0 {
		counts.CoveragePct = math.Round(float64(counts.Synced)/float64(counts.Eligible)*1000) / 10
	}
}

func kindRank(kind string) int {
	switch kind {
	case "method", "func":
		return 0
	case "type":
		return 1
	case "file":
		return 2
	case "package":
		return 3
	case "module":
		return 4
	}
	return 5
}

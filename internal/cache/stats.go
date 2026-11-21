package cache

import "context"

// SummaryRow is a compact view of one cached summary.
type SummaryRow struct {
	ContentHash    string `json:"content_hash"`
	Kind           string `json:"kind"`
	ModelID        string `json:"model_id,omitempty"`
	SummaryJSON    string `json:"summary_json"`
	CreatedAt      string `json:"created_at"`
	LastAccessedAt string `json:"last_accessed_at"`
}

// Stats is a readable snapshot of the populated cache.
type Stats struct {
	Path                   string         `json:"path"`
	PromptVersion          string         `json:"prompt_version"`
	SchemaVersion          int            `json:"schema_version"`
	Tables                 map[string]int `json:"tables"`
	WorkingSymbolsByKind   map[string]int `json:"working_symbols_by_kind"`
	WorkingSymbolsByModule map[string]int `json:"working_symbols_by_module"`
	EligibleSymbols        int            `json:"eligible_symbols"`
	DeadSymbols            int            `json:"dead_symbols"`
	WorkingEdges           int            `json:"working_edges"`
	CachedSummariesByKind  map[string]int `json:"cached_summaries_by_kind"`
	CachedSummaries        int            `json:"cached_summaries"`
	SummaryStates          map[string]int `json:"summary_states"`
	RecentSummaries        []SummaryRow   `json:"recent_summaries"`
	SymbolHistory          int            `json:"symbol_history"`
}

// Stats inspects the populated database without modifying it.
func (c *Cache) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{
		Path:                   c.path,
		PromptVersion:          PromptVersion,
		Tables:                 map[string]int{},
		WorkingSymbolsByKind:   map[string]int{},
		WorkingSymbolsByModule: map[string]int{},
		CachedSummariesByKind:  map[string]int{},
		SummaryStates:          map[string]int{},
		RecentSummaries:        make([]SummaryRow, 0),
	}
	if err := c.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&stats.SchemaVersion); err != nil {
		return Stats{}, err
	}
	for _, table := range []string{"working_symbols", "working_edges", "summary_cache", "symbol_history"} {
		var count int
		if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return Stats{}, err
		}
		stats.Tables[table] = count
	}
	stats.WorkingEdges = stats.Tables["working_edges"]
	stats.CachedSummaries = stats.Tables["summary_cache"]
	stats.SymbolHistory = stats.Tables["symbol_history"]

	if err := scanCounts(ctx, c, "SELECT kind, COUNT(*) FROM working_symbols GROUP BY kind", stats.WorkingSymbolsByKind); err != nil {
		return Stats{}, err
	}
	if err := scanCounts(ctx, c, "SELECT module_name, COUNT(*) FROM working_symbols GROUP BY module_name", stats.WorkingSymbolsByModule); err != nil {
		return Stats{}, err
	}
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM working_symbols WHERE is_eligible = 1").Scan(&stats.EligibleSymbols); err != nil {
		return Stats{}, err
	}
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM working_symbols WHERE is_dead = 1").Scan(&stats.DeadSymbols); err != nil {
		return Stats{}, err
	}
	if err := scanCounts(ctx, c, "SELECT kind, COUNT(*) FROM summary_cache GROUP BY kind", stats.CachedSummariesByKind); err != nil {
		return Stats{}, err
	}

	stateQuery := "SELECT state, COUNT(*) FROM (SELECT " + stateExpr + " AS state FROM working_symbols w WHERE w.is_eligible = 1) GROUP BY state"
	rows, err := c.db.QueryContext(ctx, stateQuery, PromptVersion, PromptVersion, PromptVersion, PromptVersion)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			_ = rows.Close()
			return Stats{}, err
		}
		stats.SummaryStates[state] = count
	}
	if err := rows.Close(); err != nil {
		return Stats{}, err
	}

	rows, err = c.db.QueryContext(ctx, `
SELECT content_hash, kind, model_id, summary_json, created_at, last_accessed_at
FROM summary_cache ORDER BY last_accessed_at DESC LIMIT 10`)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var row SummaryRow
		if err := rows.Scan(&row.ContentHash, &row.Kind, &row.ModelID, &row.SummaryJSON, &row.CreatedAt, &row.LastAccessedAt); err != nil {
			_ = rows.Close()
			return Stats{}, err
		}
		stats.RecentSummaries = append(stats.RecentSummaries, row)
	}
	if err := rows.Close(); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func scanCounts(ctx context.Context, c *Cache, query string, target map[string]int) error {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		target[key] = count
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

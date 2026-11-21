package cache

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

const stateExpr = `CASE
    WHEN w.is_eligible = 0 THEN 'not_eligible'
    WHEN EXISTS (SELECT 1 FROM summary_cache c WHERE c.content_hash = w.content_hash AND c.prompt_version = ?) THEN 'synced'
    WHEN EXISTS (
        SELECT 1 FROM symbol_history h
        JOIN summary_cache c ON c.content_hash = h.last_content_hash AND c.prompt_version = h.prompt_version
        WHERE h.symbol_id = w.id AND h.prompt_version = ? AND c.self_hash = w.self_hash
    ) THEN 'soft_stale'
    WHEN w.self_hash <> '' AND w.kind IN ('func','method','type') AND EXISTS (
        SELECT 1 FROM summary_cache c
        WHERE c.self_hash = w.self_hash AND c.prompt_version = ? AND c.kind = w.kind
    ) THEN 'soft_stale'
    WHEN EXISTS (
        SELECT 1 FROM symbol_history h
        JOIN summary_cache c ON c.content_hash = h.last_content_hash AND c.prompt_version = h.prompt_version
        WHERE h.symbol_id = w.id AND h.prompt_version = ?
    ) THEN 'hard_stale'
    ELSE 'missing'
END`

type Dependency struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	SyncState SyncState       `json:"sync_state"`
	Exported  bool            `json:"is_exported"`
	Eligible  bool            `json:"is_eligible"`
	Summary   json.RawMessage `json:"summary,omitempty"`
	Source    string          `json:"source,omitempty"`
}

type SummaryContext struct {
	Symbol              WorkingSymbol   `json:"symbol"`
	SyncState           SyncState       `json:"sync_state"`
	PromptVersion       string          `json:"prompt_version"`
	Schema              map[string]any  `json:"schema"`
	SelfHash            string          `json:"self_hash"`
	DepHash             string          `json:"dep_hash"`
	ContentHash         string          `json:"content_hash"`
	DepsChanged         bool            `json:"deps_changed,omitempty"`
	Reason              string          `json:"reason,omitempty"`
	ChangedDependencies []string        `json:"changed_dependencies,omitempty"`
	PreviousSummary     json.RawMessage `json:"previous_summary,omitempty"`
	Source              string          `json:"source,omitempty"`
	Doc                 string          `json:"doc,omitempty"`
	Signature           string          `json:"signature,omitempty"`
	Directives          []string        `json:"directives,omitempty"`
	Dependencies        []Dependency    `json:"dependencies,omitempty"`
	Note                string          `json:"note,omitempty"`
}

// StateOf classifies one working symbol.
func (c *Cache) StateOf(ctx context.Context, id string) (SyncState, error) {
	var state string
	err := c.db.QueryRowContext(ctx, "SELECT "+stateExpr+" FROM working_symbols w WHERE w.id = ?",
		PromptVersion, PromptVersion, PromptVersion, PromptVersion, id).Scan(&state)
	if err == sql.ErrNoRows {
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
		stateExpr + " AS state FROM working_symbols w WHERE w.is_eligible = 1"
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
	query := "SELECT w.kind, w.module_name, w.is_eligible, " + stateExpr + " AS state FROM working_symbols w"
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

// SummaryContext gathers source, schema, staleness, and dependency summaries.
func (c *Cache) SummaryContext(ctx context.Context, id string, idx *index.Index) (SummaryContext, error) {
	sym, err := c.workingSymbol(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return SummaryContext{}, fmt.Errorf("symbol not found: %s", id)
		}
		return SummaryContext{}, err
	}
	state := c.classify(ctx, sym)
	cached, cachedHash, err := c.cachedForState(ctx, sym, state)
	if err != nil {
		return SummaryContext{}, err
	}

	out := SummaryContext{
		Symbol:          *sym,
		SyncState:       state,
		PromptVersion:   PromptVersion,
		Schema:          SummarySchema(sym.Kind),
		SelfHash:        sym.SelfHash,
		DepHash:         sym.DepHash,
		ContentHash:     sym.ContentHash,
		PreviousSummary: cached,
	}
	if cachedHash != "" {
		c.touch(cachedHash)
	}
	if idx != nil {
		if meta := idx.Summary.Get(id); meta != nil {
			out.Doc = meta.Doc
			out.Signature = meta.Signature
			out.Directives = meta.Directives
		}
	}
	if state == StateNotEligible {
		out.Note = "symbol is not eligible for summarization; use raw source"
		out.Source = readSource(idx, sym)
		return out, nil
	}
	if state != StateSynced && state != StateNotEligible {
		out.Reason, out.DepsChanged = c.staleReason(ctx, sym, state)
	}
	if sym.Kind == "func" || sym.Kind == "method" || sym.Kind == "type" {
		out.Source = readSource(idx, sym)
		out.Dependencies, err = c.dependencies(ctx, sym, idx, true)
		if err != nil {
			return SummaryContext{}, err
		}
	} else {
		out.Dependencies, err = c.children(ctx, sym, idx, true)
		if err != nil {
			return SummaryContext{}, err
		}
	}
	if state == StateSoftStale || state == StateHardStale {
		for _, dep := range out.Dependencies {
			if dep.SyncState != StateSynced {
				out.ChangedDependencies = append(out.ChangedDependencies, dep.ID)
			}
		}
	}
	return out, nil
}

// Enrich returns a cached summary for existing read tools.
func (c *Cache) Enrich(ctx context.Context, id string) (json.RawMessage, SyncState, bool, error) {
	sym, err := c.workingSymbol(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
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
	err := c.db.QueryRowContext(ctx,
		"SELECT last_self_hash, last_dep_hash FROM symbol_history WHERE symbol_id = ? AND prompt_version = ?",
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
		rows, err := c.db.QueryContext(ctx,
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
		_ = rows.Close()
	} else {
		rows, err := c.db.QueryContext(ctx,
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
		_ = rows.Close()
	}
	return c.loadDependencies(ctx, ids, idx, needSummaries)
}

func (c *Cache) children(ctx context.Context, sym *WorkingSymbol, idx *index.Index, needSummaries bool) ([]Dependency, error) {
	rows, err := c.db.QueryContext(ctx,
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
	_ = rows.Close()
	return c.loadDependencies(ctx, ids, idx, needSummaries)
}

func (c *Cache) loadDependencies(ctx context.Context, ids []string, idx *index.Index, needSummaries bool) ([]Dependency, error) {
	out := make([]Dependency, 0, len(ids))
	for _, id := range ids {
		sym, err := c.workingSymbol(ctx, id)
		if err != nil {
			if err == sql.ErrNoRows {
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
	err := c.db.QueryRowContext(ctx, `
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
	err := c.db.QueryRowContext(ctx, "SELECT "+stateExpr+" FROM working_symbols w WHERE w.id = ?",
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
                SELECT h.last_content_hash FROM symbol_history h
                JOIN summary_cache c2 ON c2.content_hash = h.last_content_hash AND c2.prompt_version = h.prompt_version
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
                SELECT last_content_hash FROM symbol_history WHERE symbol_id = ? AND prompt_version = ?
            ) ORDER BY created_at DESC LIMIT 1`,
			PromptVersion, sym.ID, PromptVersion)
	default:
		return nil, "", nil
	}
}

func (c *Cache) querySummary(ctx context.Context, where string, args ...any) (json.RawMessage, string, error) {
	var hash, raw string
	err := c.db.QueryRowContext(ctx,
		"SELECT content_hash, summary_json FROM summary_cache "+where, args...).Scan(&hash, &raw)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return json.RawMessage(raw), hash, nil
}

// UpsertSummaries validates and stores client-produced summaries.
func (c *Cache) UpsertSummaries(ctx context.Context, items []SummaryInput) ([]UpsertResult, error) {
	results := make([]UpsertResult, 0, len(items))
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, item := range items {
		result := UpsertResult{SymbolID: item.SymbolID, Status: "validation_error"}
		sym, err := workingSymbolTx(ctx, tx, item.SymbolID)
		if err == sql.ErrNoRows {
			result.Status = "not_found"
			result.Error = "symbol not found in current working tree"
			results = append(results, result)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !sym.Eligible {
			result.Error = "symbol is not eligible for summarization"
			results = append(results, result)
			continue
		}
		if err := validateSummary(sym.Kind, item.Summary); err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		if sym.SelfHash != item.ExpectedSelfHash || sym.DepHash != item.ExpectedDepHash {
			result.Status = "hash_mismatch"
			result.Error = "expected hashes do not match current working tree; fetch context again"
			results = append(results, result)
			continue
		}
		raw, err := compactJSON(item.Summary)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		now := nowString()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO summary_cache (content_hash, prompt_version, self_hash, dep_hash, kind, model_id, summary_json, created_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(content_hash, prompt_version) DO UPDATE SET last_accessed_at = excluded.last_accessed_at`,
			sym.ContentHash, PromptVersion, sym.SelfHash, sym.DepHash, sym.Kind, item.ModelID, string(raw), now, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO symbol_history (symbol_id, prompt_version, last_content_hash, last_self_hash, last_dep_hash, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol_id, prompt_version) DO UPDATE SET
    last_content_hash = excluded.last_content_hash,
    last_self_hash = excluded.last_self_hash,
    last_dep_hash = excluded.last_dep_hash,
    updated_at = excluded.updated_at`,
			sym.ID, PromptVersion, sym.ContentHash, sym.SelfHash, sym.DepHash, now); err != nil {
			return nil, err
		}
		result.Status = "updated"
		results = append(results, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func workingSymbolTx(ctx context.Context, tx *sql.Tx, id string) (*WorkingSymbol, error) {
	var sym WorkingSymbol
	var exported, dead, eligible int
	err := tx.QueryRowContext(ctx, `
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

type funcSummary struct {
	Intent      *string   `json:"intent"`
	SideEffects *[]string `json:"side_effects"`
	Invariants  *[]string `json:"invariants"`
	Concurrency *string   `json:"concurrency"`
}

type archSummary struct {
	Intent        *string   `json:"intent"`
	KeyInvariants *[]string `json:"key_invariants"`
	Boundaries    *[]string `json:"boundaries"`
}

var sideEffectEnum = map[string]bool{
	"none": true, "db_read": true, "db_write": true, "network_io": true,
	"fs_io": true, "exec": true, "mutates_receiver": true, "mutates_global": true,
	"spawns_goroutine": true, "panics": true, "env_read": true,
}

func validateSummary(kind string, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("summary is required")
	}
	if kind == "func" || kind == "method" {
		var payload funcSummary
		if err := strictDecode(raw, &payload); err != nil {
			return err
		}
		if err := validateIntent(payload.Intent); err != nil {
			return err
		}
		if payload.SideEffects == nil || len(*payload.SideEffects) == 0 {
			return fmt.Errorf("side_effects must be a non-empty array")
		}
		none := false
		for i, effect := range *payload.SideEffects {
			if !sideEffectEnum[effect] {
				return fmt.Errorf("side_effects[%d]: invalid value %q", i, effect)
			}
			if effect == "none" {
				none = true
			}
		}
		if none && len(*payload.SideEffects) != 1 {
			return fmt.Errorf("side_effects: 'none' must be the sole element")
		}
		if payload.Invariants == nil {
			return fmt.Errorf("invariants must be an array (may be empty)")
		}
		if err := validateStrings(*payload.Invariants, "invariants"); err != nil {
			return err
		}
		if payload.Concurrency != nil && utf8.RuneCountInString(*payload.Concurrency) > 800 {
			return fmt.Errorf("concurrency exceeds 800 characters")
		}
		return nil
	}

	var payload archSummary
	if err := strictDecode(raw, &payload); err != nil {
		return err
	}
	if err := validateIntent(payload.Intent); err != nil {
		return err
	}
	if payload.KeyInvariants == nil {
		return fmt.Errorf("key_invariants must be an array (may be empty)")
	}
	if payload.Boundaries == nil {
		return fmt.Errorf("boundaries must be an array (may be empty)")
	}
	if err := validateStrings(*payload.KeyInvariants, "key_invariants"); err != nil {
		return err
	}
	return validateStrings(*payload.Boundaries, "boundaries")
}

func strictDecode(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid summary JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid summary JSON: trailing data")
	}
	return nil
}

func validateIntent(intent *string) error {
	if intent == nil || strings.TrimSpace(*intent) == "" {
		return fmt.Errorf("intent is required and must not be empty")
	}
	if utf8.RuneCountInString(*intent) > 400 {
		return fmt.Errorf("intent exceeds 400 characters")
	}
	return nil
}

func validateStrings(values []string, field string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s[%d] must not be empty", field, i)
		}
		if utf8.RuneCountInString(value) > 400 {
			return fmt.Errorf("%s[%d] exceeds 400 characters", field, i)
		}
	}
	return nil
}

func compactJSON(raw json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readSource(idx *index.Index, sym *WorkingSymbol) string {
	if idx == nil || sym == nil || sym.FilePath == "" || sym.StartLine <= 0 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.FilePath)))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	start := max(sym.StartLine-1, 0)
	end := sym.EndLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

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
	if err == sql.ErrNoRows {
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
			"SELECT model_id FROM summary_cache WHERE content_hash = ? AND prompt_version = ?",
			hash, PromptVersion).Scan(&out.ModelID); err != nil && err != sql.ErrNoRows {
			return Cached{}, err
		}
	}
	return out, nil
}

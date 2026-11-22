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

	"github.com/yanulabs/goray/internal/index"
)

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

	ParentPackageID            string    `json:"parent_package_id,omitempty"`
	ParentModuleID             string    `json:"parent_module_id,omitempty"`
	ParentPackageSummaryCached *bool     `json:"parent_package_summary_cached,omitempty"`
	ParentModuleSummaryCached  *bool     `json:"parent_module_summary_cached,omitempty"`
	ParentPackageSyncState     SyncState `json:"parent_package_sync_state,omitempty"`
	ParentModuleSyncState      SyncState `json:"parent_module_sync_state,omitempty"`

	Module *ModuleContext `json:"module_context,omitempty"`
}

// ModuleContext is the compact architectural context returned for mod: targets.
type ModuleContext struct {
	Name                   string              `json:"name"`
	ProdLOC                int                 `json:"prod_loc"`
	TestLOC                int                 `json:"test_loc"`
	Packages               int                 `json:"packages"`
	Files                  int                 `json:"files"`
	PackageRollups         []ModulePackage     `json:"package_rollups"`
	DependencyEdges        ModuleEdges         `json:"dependency_edges"`
	ExportedAPI            []ModuleAPI         `json:"exported_api,omitempty"`
	KeyFiles               []ModuleFileExcerpt `json:"key_files,omitempty"`
	CachedPackageSummaries []string            `json:"cached_package_summaries,omitempty"`
	Truncated              bool                `json:"truncated"`
}

// ModulePackage is one package rollup inside a module summary context.
type ModulePackage struct {
	ID        string   `json:"id"`
	PkgPath   string   `json:"pkg_path"`
	RelPath   string   `json:"rel_path"`
	Name      string   `json:"name"`
	ProdFiles int      `json:"prod_files"`
	Files     int      `json:"files"`
	ProdLOC   int      `json:"prod_loc"`
	TestLOC   int      `json:"test_loc"`
	Exported  int      `json:"exported"`
	TestOnly  bool     `json:"test_only,omitempty"`
	Imports   []string `json:"imports,omitempty"`
	Doc       string   `json:"doc,omitempty"`
}

// ModuleEdge is one incoming or outgoing module dependency edge.
type ModuleEdge struct {
	Module  string `json:"module"`
	Symbols int    `json:"symbols"`
}

// ModuleEdges is the module-level dependency edge projection.
type ModuleEdges struct {
	Out []ModuleEdge `json:"out"`
	In  []ModuleEdge `json:"in"`
}

// ModuleAPI is one exported declaration inside a module.
type ModuleAPI struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	PkgPath   string `json:"pkg_path"`
	Signature string `json:"signature,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
}

// ModuleFileExcerpt is a bounded excerpt of one key production file.
type ModuleFileExcerpt struct {
	Path      string `json:"path"`
	LOC       int    `json:"loc"`
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated,omitempty"`
}

// StateOf classifies one working symbol.
func (c *Cache) StateOf(ctx context.Context, id string) (SyncState, error) {
	var state string
	err := c.db.QueryRowContext(ctx, "SELECT "+c.stateExpr()+" FROM working_symbols w WHERE w.id = ?",
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
		out.Note = "symbol is not eligible for summarization; use the parent package/module summary or raw source"
		out.Source = readSource(idx, sym)
		c.populateSummaryParents(ctx, sym, &out)
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
	if sym.Kind == "module" {
		c.populateModuleContext(sym, idx, &out)
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

func (c *Cache) populateSummaryParents(ctx context.Context, sym *WorkingSymbol, out *SummaryContext) {
	if sym.PkgPath != "" {
		out.ParentPackageID = "pkg:" + sym.PkgPath
	}
	if sym.ModuleName != "" {
		out.ParentModuleID = "mod:" + sym.ModuleName
	}
	stateFor := func(id string) (bool, SyncState) {
		if id == "" {
			return false, ""
		}
		parent, err := c.workingSymbol(ctx, id)
		if err != nil {
			return false, ""
		}
		state := c.classify(ctx, parent)
		cached, _, err := c.cachedForState(ctx, parent, state)
		if err != nil {
			return false, state
		}
		return len(cached) > 0, state
	}
	if cached, state := stateFor(out.ParentPackageID); state != "" {
		out.ParentPackageSummaryCached = &cached
		out.ParentPackageSyncState = state
	}
	if cached, state := stateFor(out.ParentModuleID); state != "" {
		out.ParentModuleSummaryCached = &cached
		out.ParentModuleSyncState = state
	}
}

const (
	moduleAPILimit        = 40
	moduleKeyFileLimit    = 3
	moduleKeyFileMaxRunes = 1200
	modulePackageDocRunes = 300
)

// populateModuleContext adds compact package rollups, module edges, exported
// API signatures, key file excerpts, and cached child package summary IDs for
// mod: targets. The payload is bounded so a direct get_summary_context call
// stays small enough to feed into upsert_summaries without extra probing.
func (c *Cache) populateModuleContext(sym *WorkingSymbol, idx *index.Index, out *SummaryContext) {
	if sym == nil || idx == nil {
		return
	}
	mod := idx.Modules[sym.ModuleName]
	if mod == nil {
		return
	}
	ctx := ModuleContext{
		Name: sym.ModuleName, ProdLOC: mod.ProdLOC, TestLOC: mod.TestLOC,
		Packages: mod.Packages, Files: mod.Files,
		DependencyEdges: ModuleEdges{Out: moduleEdgeList(mod.OutModules), In: moduleEdgeList(mod.InModules)},
	}
	for _, pkg := range idx.Packages {
		if pkg.ModuleArea != sym.ModuleName {
			continue
		}
		rollup := ModulePackage{
			ID: "pkg:" + pkg.PkgPath, PkgPath: pkg.PkgPath, RelPath: pkg.RelPath, Name: pkg.Name,
			ProdFiles: pkg.ProdFiles, Files: pkg.Files, ProdLOC: pkg.ProdLOC, TestLOC: pkg.TestLOC,
			Exported: pkg.Exported, TestOnly: pkg.TestOnly, Imports: pkg.Imports,
		}
		if meta := idx.Summary.Get("pkg:" + pkg.PkgPath); meta != nil {
			if doc, cut := truncateModuleRunes(meta.Doc, modulePackageDocRunes); doc != "" {
				rollup.Doc = doc
				if cut {
					rollup.Doc += "..."
				}
			}
		}
		ctx.PackageRollups = append(ctx.PackageRollups, rollup)
	}
	sort.Slice(ctx.PackageRollups, func(i, j int) bool {
		if ctx.PackageRollups[i].RelPath != ctx.PackageRollups[j].RelPath {
			return ctx.PackageRollups[i].RelPath < ctx.PackageRollups[j].RelPath
		}
		return ctx.PackageRollups[i].ID < ctx.PackageRollups[j].ID
	})

	api := make([]ModuleAPI, 0)
	files := make([]*index.SummarySymbol, 0)
	if idx.Summary != nil {
		for _, meta := range idx.Summary.Symbols {
			if meta.ModuleName != sym.ModuleName {
				continue
			}
			switch meta.Kind {
			case "module", "package":
				continue
			case "file":
				if !meta.IsTest && !meta.IsGenerated && meta.LOC > 0 {
					files = append(files, meta)
				}
				continue
			}
			if !meta.Exported || meta.IsTest || meta.IsGenerated {
				continue
			}
			api = append(api, ModuleAPI{
				ID: meta.ID, Kind: meta.Kind, PkgPath: meta.PkgPath,
				Signature: meta.Signature, FilePath: meta.FilePath, StartLine: meta.StartLine,
			})
		}
	}
	sort.Slice(api, func(i, j int) bool {
		if api[i].PkgPath != api[j].PkgPath {
			return api[i].PkgPath < api[j].PkgPath
		}
		return api[i].ID < api[j].ID
	})
	if len(api) > moduleAPILimit {
		api = api[:moduleAPILimit]
		ctx.Truncated = true
	}
	ctx.ExportedAPI = api

	sort.Slice(files, func(i, j int) bool {
		if files[i].LOC != files[j].LOC {
			return files[i].LOC > files[j].LOC
		}
		return files[i].FilePath < files[j].FilePath
	})
	if len(files) > moduleKeyFileLimit {
		files = files[:moduleKeyFileLimit]
		ctx.Truncated = true
	}
	for _, file := range files {
		excerpt := readSource(idx, &WorkingSymbol{FilePath: file.FilePath, StartLine: 1, EndLine: file.EndLine})
		if excerpt == "" {
			continue
		}
		text, cut := truncateModuleRunes(excerpt, moduleKeyFileMaxRunes)
		if cut {
			text += "..."
			ctx.Truncated = true
		}
		ctx.KeyFiles = append(ctx.KeyFiles, ModuleFileExcerpt{Path: file.FilePath, LOC: file.LOC, Excerpt: text, Truncated: cut})
	}

	for _, dep := range out.Dependencies {
		if dep.Kind == "package" && len(dep.Summary) > 0 {
			ctx.CachedPackageSummaries = append(ctx.CachedPackageSummaries, dep.ID)
		}
	}
	out.Module = &ctx
}

func moduleEdgeList(edges map[string][]string) []ModuleEdge {
	keys := make([]string, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ModuleEdge, 0, len(keys))
	for _, key := range keys {
		out = append(out, ModuleEdge{Module: key, Symbols: len(edges[key])})
	}
	return out
}

func truncateModuleRunes(text string, limit int) (string, bool) {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:limit]), true
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
	err := c.db.QueryRowContext(ctx, "SELECT "+c.stateExpr()+" FROM working_symbols w WHERE w.id = ?",
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
	default:
		return nil, "", nil
	}
}

func (c *Cache) querySummary(ctx context.Context, where string, args ...any) (json.RawMessage, string, error) {
	var hash, raw string
	err := c.db.QueryRowContext(ctx,
		"SELECT content_hash, summary_json FROM "+c.summaryTable("summary_cache")+" "+where, args...).Scan(&hash, &raw)
	if err == sql.ErrNoRows {
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
		if err == sql.ErrNoRows {
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
			"SELECT model_id FROM "+c.summaryTable("summary_cache")+" WHERE content_hash = ? AND prompt_version = ?",
			hash, PromptVersion).Scan(&out.ModelID); err != nil && err != sql.ErrNoRows {
			return Cached{}, err
		}
	}
	return out, nil
}

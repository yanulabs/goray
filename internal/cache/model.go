package cache

import "encoding/json"

// SyncState is the freshness of a symbol's cached summary.
type SyncState string

// SyncState values.
const (
	StateSynced      SyncState = "synced"
	StateSoftStale   SyncState = "soft_stale"
	StateHardStale   SyncState = "hard_stale"
	StateMissing     SyncState = "missing"
	StateNotEligible SyncState = "not_eligible"
)

// WorkingSymbol mirrors a row of working_symbols.
type WorkingSymbol struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id,omitempty"`
	Kind        string `json:"kind"`
	ModuleName  string `json:"module_name,omitempty"`
	PkgPath     string `json:"pkg_path,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	LOC         int    `json:"loc"`
	Complexity  int    `json:"complexity"`
	FanIn       int    `json:"fan_in"`
	Exported    bool   `json:"is_exported"`
	Dead        bool   `json:"is_dead"`
	Eligible    bool   `json:"is_eligible"`
	SigHash     string `json:"sig_hash"`
	SelfHash    string `json:"self_hash"`
	DepHash     string `json:"dep_hash"`
	ContentHash string `json:"content_hash"`
}

// StaleFilter selects stale summary candidates.
type StaleFilter struct {
	Kind        string
	Module      string
	PkgPath     string
	State       SyncState
	Limit       int
	IncludeDead bool
}

// StaleSymbol is one list_stale_summaries row.
type StaleSymbol struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	ModuleName  string    `json:"module_name,omitempty"`
	PkgPath     string    `json:"pkg_path,omitempty"`
	FilePath    string    `json:"file_path,omitempty"`
	SyncState   SyncState `json:"sync_state"`
	SelfHash    string    `json:"self_hash"`
	DepHash     string    `json:"dep_hash"`
	ContentHash string    `json:"content_hash"`
	LOC         int       `json:"loc"`
	Complexity  int       `json:"complexity"`
	FanIn       int       `json:"fan_in"`
}

// SummaryInput is one client-submitted summary.
type SummaryInput struct {
	SymbolID         string          `json:"symbol_id"`
	Summary          json.RawMessage `json:"summary"`
	ExpectedSelfHash string          `json:"expected_self_hash"`
	ExpectedDepHash  string          `json:"expected_dep_hash"`
	ModelID          string          `json:"model_id,omitempty"`
}

// UpsertResult reports one item's outcome.
type UpsertResult struct {
	SymbolID string `json:"symbol_id"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// CoverageCounts aggregates summary coverage.
type CoverageCounts struct {
	Total       int     `json:"total"`
	Eligible    int     `json:"eligible"`
	NotEligible int     `json:"not_eligible"`
	Synced      int     `json:"synced"`
	SoftStale   int     `json:"soft_stale"`
	HardStale   int     `json:"hard_stale"`
	Missing     int     `json:"missing"`
	CoveragePct float64 `json:"coverage_pct"`
}

// CoverageGroup is one module/kind slice of summary coverage.
type CoverageGroup struct {
	Module string `json:"module"`
	Kind   string `json:"kind"`
	CoverageCounts
}

// Coverage is the summary-cache coverage report.
type Coverage struct {
	PromptVersion string          `json:"prompt_version"`
	Groups        []CoverageGroup `json:"groups"`
	Totals        CoverageCounts  `json:"totals"`
}

// SummarySchema is the exact expected JSON schema for a symbol kind.
func SummarySchema(kind string) map[string]any {
	if kind == "func" || kind == "method" {
		return map[string]any{
			"type":     "object",
			"required": []string{"intent", "side_effects", "invariants"},
			"properties": map[string]any{
				"intent":       map[string]any{"type": "string", "minLength": 1, "maxLength": 400},
				"side_effects": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"none", "db_read", "db_write", "network_io", "fs_io", "exec", "mutates_receiver", "mutates_global", "spawns_goroutine", "panics", "env_read"}}},
				"invariants":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"concurrency":  map[string]any{"type": "string"},
			},
		}
	}
	return map[string]any{
		"type":     "object",
		"required": []string{"intent", "key_invariants", "boundaries"},
		"properties": map[string]any{
			"intent":         map[string]any{"type": "string", "minLength": 1, "maxLength": 400},
			"key_invariants": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"boundaries":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

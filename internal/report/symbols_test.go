package report

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func TestBuildSymbolRowsOrderingAndReferences(t *testing.T) {
	const (
		alphaID = "sym:example.com/p.Alpha"
		zuluID  = "sym:example.com/p.Zulu"
	)
	idx := &index.Index{
		Summary: &index.SummaryData{Symbols: []*index.SummarySymbol{
			{ID: zuluID, Kind: "func"},
			{ID: alphaID, Kind: "func", Eligible: true},
		}},
		Symbols: map[string]*index.Symbol{
			"old-alpha": {ID: "old-alpha", Name: "Alpha", Kind: "func", PkgPath: "example.com/p", Signature: "func Alpha()"},
			"old-zulu":  {ID: "old-zulu", Name: "Zulu", Kind: "func", PkgPath: "example.com/p"},
		},
		Callers:     map[string][]string{"old-alpha": {"old-zulu", "missing"}},
		Callees:     map[string][]string{"old-alpha": {"old-zulu"}},
		TestCovered: map[string]bool{"old-alpha": true},
	}
	canonical, canonicalByExisting := canonicalSymbols(idx)
	cached := map[string]cache.Cached{alphaID: {State: cache.StateSynced}}
	rows := buildSymbolRows(idx, cached, canonical, canonicalByExisting)
	if len(rows) != 2 || rows[0].ID != alphaID || rows[1].ID != zuluID {
		t.Fatalf("rows not ordered by ID: %+v", rows)
	}
	alpha := rows[0]
	if alpha.Name != "Alpha" || alpha.SyncState != "synced" || alpha.FanOut != 1 {
		t.Fatalf("alpha metadata = %+v", alpha)
	}
	if alpha.TestCovered == nil || !*alpha.TestCovered {
		t.Fatalf("alpha test coverage = %v", alpha.TestCovered)
	}
	if !reflect.DeepEqual(alpha.Callers, []string{zuluID}) || !reflect.DeepEqual(alpha.Callees, []string{zuluID}) {
		t.Fatalf("alpha references: callers=%v callees=%v", alpha.Callers, alpha.Callees)
	}
	if rows[1].SyncState != "not_eligible" {
		t.Fatalf("zulu state = %q", rows[1].SyncState)
	}
}

func TestBuildSymbolRowMissingMetadata(t *testing.T) {
	summary := &index.SummarySymbol{
		ID: "sym:example.com/p.Missing", Kind: "func", FilePath: "missing.go",
		Signature: "func Missing()", Doc: "Missing docs.", Eligible: true,
	}
	idx := &index.Index{
		Summary:        &index.SummaryData{Symbols: []*index.SummarySymbol{summary}},
		UntrackedFiles: map[string]bool{"missing.go": true},
	}
	rows := buildSymbolRows(idx, nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Name != "Missing" || !row.Untracked || row.TestCovered != nil || row.FanOut != 0 {
		t.Fatalf("fallback metadata = %+v", row)
	}
	if row.Signature != summary.Signature || row.Doc != summary.Doc || row.SyncState != "missing" || row.Summary != nil {
		t.Fatalf("fallback summary metadata = %+v", row)
	}
	if row.Callers != nil || row.Callees != nil {
		t.Fatalf("missing symbol references should stay nil: %+v", row)
	}
}

func TestCanonicalRefs(t *testing.T) {
	got := canonicalRefs([]string{"old-z", "missing", "old-a", "old-z"}, map[string]string{
		"old-a": "sym:a", "old-z": "sym:z",
	})
	want := []string{"sym:a", "sym:z", "sym:z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonicalRefs = %v, want %v", got, want)
	}
	if empty := canonicalRefs(nil, nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty canonicalRefs = %#v", empty)
	}
}

func TestSymbolLabel(t *testing.T) {
	tests := []struct {
		name    string
		summary *index.SummarySymbol
		sym     *index.Symbol
		want    string
	}{
		{"symbol wins", &index.SummarySymbol{ID: "id", Kind: "module", ModuleName: "module"}, &index.Symbol{Name: "Symbol"}, "Symbol"},
		{"module", &index.SummarySymbol{Kind: "module", ModuleName: "module"}, nil, "module"},
		{"package", &index.SummarySymbol{Kind: "package", PkgPath: "example.com/p"}, nil, "example.com/p"},
		{"file", &index.SummarySymbol{Kind: "file", FilePath: "p.go"}, nil, "p.go"},
		{"qualified ID", &index.SummarySymbol{ID: "sym:example.com/p.F", Kind: "func"}, nil, "F"},
		{"bare ID", &index.SummarySymbol{ID: "bare", Kind: "var"}, nil, "bare"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := symbolLabel(tc.summary, tc.sym); got != tc.want {
				t.Fatalf("symbolLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFanOutMetadataFallbacks(t *testing.T) {
	idx := &index.Index{
		Callees:   map[string][]string{"call": {"a", "b"}},
		TypeImpls: map[string][]index.ImplMatch{"type": {{TypeID: "impl"}}},
		Modules:   map[string]*index.ModSummary{"m": {OutModules: map[string][]string{"a": nil, "b": nil}}},
		Packages:  map[string]*index.PkgInfo{"example.com/p": {Imports: []string{"a"}}},
	}
	tests := []struct {
		name    string
		summary *index.SummarySymbol
		sym     *index.Symbol
		want    int
	}{
		{"func", &index.SummarySymbol{}, &index.Symbol{ID: "call", Kind: "func"}, 2},
		{"method", &index.SummarySymbol{}, &index.Symbol{ID: "call", Kind: "method"}, 2},
		{"type", &index.SummarySymbol{}, &index.Symbol{ID: "type", Kind: "type"}, 1},
		{"interface", &index.SummarySymbol{}, &index.Symbol{ID: "type", Kind: "interface"}, 1},
		{"struct", &index.SummarySymbol{}, &index.Symbol{ID: "type", Kind: "struct"}, 1},
		{"module", &index.SummarySymbol{Kind: "module", ModuleName: "m"}, nil, 2},
		{"package", &index.SummarySymbol{Kind: "package", PkgPath: "example.com/p"}, nil, 1},
		{"missing module", &index.SummarySymbol{Kind: "module", ModuleName: "missing"}, nil, 0},
		{"unknown", &index.SummarySymbol{Kind: "var"}, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fanOut(idx, tc.summary, tc.sym); got != tc.want {
				t.Fatalf("fanOut = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSymbolMetadataHelpers(t *testing.T) {
	idx := &index.Index{
		UntrackedFiles: map[string]bool{"tracked.go": false, "untracked.go": true},
		TestCovered:    map[string]bool{"covered": true, "type": true},
	}
	if untrackedFor(idx, &index.SummarySymbol{FilePath: "untracked.go"}, &index.Symbol{Untracked: false}) {
		t.Fatal("symbol metadata should override file fallback")
	}
	if !untrackedFor(idx, &index.SummarySymbol{FilePath: "tracked.go"}, &index.Symbol{Untracked: true}) {
		t.Fatal("untracked symbol was ignored")
	}
	if !untrackedFor(idx, &index.SummarySymbol{FilePath: "untracked.go"}, nil) {
		t.Fatal("untracked file fallback was ignored")
	}

	falseValue, trueValue := false, true
	coverageTests := []struct {
		name string
		sym  *index.Symbol
		want *bool
	}{
		{"missing symbol", nil, nil},
		{"non-function", &index.Symbol{ID: "type", Kind: "type"}, nil},
		{"uncovered function", &index.Symbol{ID: "uncovered", Kind: "func"}, &falseValue},
		{"covered method", &index.Symbol{ID: "covered", Kind: "method"}, &trueValue},
	}
	for _, tc := range coverageTests {
		t.Run(tc.name, func(t *testing.T) {
			if got := testCoveredFor(idx, tc.sym); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("testCoveredFor = %v, want %v", got, tc.want)
			}
		})
	}

	if got := signatureFor(&index.SummarySymbol{Signature: "summary"}, &index.Symbol{Signature: "symbol"}); got != "summary" {
		t.Fatalf("summary signature precedence = %q", got)
	}
	if got := signatureFor(&index.SummarySymbol{}, &index.Symbol{Signature: "symbol"}); got != "symbol" {
		t.Fatalf("symbol signature fallback = %q", got)
	}
	if got := signatureFor(&index.SummarySymbol{}, nil); got != "" {
		t.Fatalf("empty signature fallback = %q", got)
	}
}

func TestSummaryStateFallbacks(t *testing.T) {
	eligible := &index.SummarySymbol{ID: "eligible", Eligible: true}
	ineligible := &index.SummarySymbol{ID: "ineligible"}
	tests := []struct {
		name    string
		cached  map[string]cache.Cached
		summary *index.SummarySymbol
		want    string
	}{
		{"eligible missing", nil, eligible, "missing"},
		{"ineligible missing", nil, ineligible, "not_eligible"},
		{"empty eligible state", map[string]cache.Cached{"eligible": {}}, eligible, "missing"},
		{"empty ineligible state", map[string]cache.Cached{"ineligible": {}}, ineligible, "not_eligible"},
		{"cached state wins", map[string]cache.Cached{"ineligible": {State: cache.StateHardStale}}, ineligible, "hard_stale"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stateFor(tc.cached, tc.summary); got != tc.want {
				t.Fatalf("stateFor = %q, want %q", got, tc.want)
			}
		})
	}
	if got := stateForID(nil, "absent", nil); got != "missing" {
		t.Fatalf("stateForID without metadata = %q", got)
	}
	if got := stateForID(nil, eligible.ID, eligible); got != "missing" {
		t.Fatalf("stateForID with metadata = %q", got)
	}
}

func TestSummaryViewInvalidMetadata(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage(`{"intent":`)} {
		if got := summaryView(cache.Cached{Summary: raw}); got != nil {
			t.Fatalf("summaryView(%q) = %+v, want nil", raw, got)
		}
	}
	item := cache.Cached{
		Summary: json.RawMessage(`{"intent":"Intent","side_effects":["db_write"],"invariants":["Invariant"],"concurrency":"Concurrent","key_invariants":["Key"],"boundaries":["Boundary"]}`),
		State:   cache.StateSoftStale, ModelID: "model", DepsChanged: true,
	}
	want := &render.HTMLSummary{
		Intent: "Intent", SideEffects: []string{"db_write"}, Invariants: []string{"Invariant"},
		Concurrency: "Concurrent", KeyInvariants: []string{"Key"}, Boundaries: []string{"Boundary"},
		SyncState: "soft_stale", ModelID: "model", DepsChanged: true,
	}
	if got := summaryView(item); !reflect.DeepEqual(got, want) {
		t.Fatalf("summaryView = %+v, want %+v", got, want)
	}
}

package diff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func TestCompareAPIUsesCanonicalIDsAndStableOrdering(t *testing.T) {
	symbol := func(name, signature string) *index.Symbol {
		return &index.Symbol{
			ID: "example.com/m." + name, Name: name, Kind: "func", PkgPath: "example.com/m",
			Exported: true, Signature: signature, File: "api.go", Line: 1,
		}
	}
	base := &index.Index{Symbols: map[string]*index.Symbol{
		"example.com/m.Removed":  symbol("Removed", "func Removed()"),
		"example.com/m.Stable":   symbol("Stable", "func Stable()"),
		"example.com/m.Changed":  symbol("Changed", "func Changed(int)"),
		"example.com/m.Private":  {ID: "example.com/m.Private", Name: "Private", Kind: "func", PkgPath: "example.com/m"},
		"example.com/m.TestOnly": {ID: "example.com/m.TestOnly", Name: "TestOnly", Kind: "func", PkgPath: "example.com/m", Exported: true, IsTest: true},
	}}
	head := &index.Index{Symbols: map[string]*index.Symbol{
		"example.com/m.Added":    symbol("Added", "func Added()"),
		"example.com/m.Stable":   symbol("Stable", "func Stable()"),
		"example.com/m.Changed":  symbol("Changed", "func Changed(string)"),
		"example.com/m.Private":  {ID: "example.com/m.Private", Name: "Private", Kind: "func", PkgPath: "example.com/m"},
		"example.com/m.TestOnly": {ID: "example.com/m.TestOnly", Name: "TestOnly", Kind: "func", PkgPath: "example.com/m", Exported: true, IsTest: true},
	}}

	want := API{
		Removed:  []APIChange{{ID: "sym:example.com/m.Removed", Name: "Removed", Kind: "func", File: "api.go", Line: 1, Before: "func Removed()"}},
		Modified: []APIChange{{ID: "sym:example.com/m.Changed", Name: "Changed", Kind: "func", File: "api.go", Line: 1, Before: "func Changed(int)", After: "func Changed(string)"}},
		Added:    []APIChange{{ID: "sym:example.com/m.Added", Name: "Added", Kind: "func", File: "api.go", Line: 1, Before: "func Added()"}},
	}
	for range 20 {
		if got := compareAPI(base, head); !reflect.DeepEqual(got, want) {
			t.Fatalf("compareAPI = %+v, want %+v", got, want)
		}
	}
}

func TestCompareHotspotsFiltersCoverageAndComputesDeltas(t *testing.T) {
	symbol := func(id string, complexity, loc int) *index.Symbol {
		return &index.Symbol{ID: id, Name: id, Kind: "func", Cyclomatic: complexity, LOC: loc}
	}
	base := &index.Index{Symbols: map[string]*index.Symbol{
		"changed": symbol("changed", 10, 10),
		"same":    symbol("same", 10, 10),
	}}
	head := &index.Index{
		Symbols: map[string]*index.Symbol{
			"new-high": symbol("new-high", 20, 5),
			"new-low":  symbol("new-low", 10, 5),
			"covered":  symbol("covered", 30, 5),
			"changed":  symbol("changed", 12, 15),
			"same":     symbol("same", 10, 10),
			"simple":   symbol("simple", 9, 100),
			"test":     {ID: "test", Name: "test", Kind: "func", Cyclomatic: 30, LOC: 5, IsTest: true},
		},
		TestCovered: map[string]bool{"covered": true},
	}

	got := compareHotspots(base, head)
	if len(got.New) != 2 || got.New[0].ID != "new-high" || got.New[1].ID != "new-low" {
		t.Fatalf("new hotspots = %+v", got.New)
	}
	wantDelta := []Hotspot{{
		ID: "changed", Name: "changed", AfterCC: 12, AfterLOC: 15,
		BeforeCC: 10, BeforeLOC: 10, DeltaCC: 2, DeltaLOC: 5,
	}}
	if !reflect.DeepEqual(got.TopDelta, wantDelta) {
		t.Fatalf("hotspot deltas = %+v, want %+v", got.TopDelta, wantDelta)
	}
}

func TestCompareHotspotsCapsDeltasAfterSorting(t *testing.T) {
	base := &index.Index{Symbols: map[string]*index.Symbol{}}
	head := &index.Index{Symbols: map[string]*index.Symbol{}, TestCovered: map[string]bool{}}
	for i := range 12 {
		id := fmt.Sprintf("fn-%02d", i)
		base.Symbols[id] = &index.Symbol{ID: id, Name: id, Kind: "func", Cyclomatic: 10, LOC: 10}
		head.Symbols[id] = &index.Symbol{ID: id, Name: id, Kind: "func", Cyclomatic: 10, LOC: 11 + i}
	}
	got := compareHotspots(base, head)
	if len(got.TopDelta) != maxDiffHotspotDeltas {
		t.Fatalf("delta count = %d, want %d", len(got.TopDelta), maxDiffHotspotDeltas)
	}
	for i := 1; i < len(got.TopDelta); i++ {
		if got.TopDelta[i-1].DeltaLOC < got.TopDelta[i].DeltaLOC {
			t.Fatalf("deltas not sorted descending: %+v", got.TopDelta)
		}
	}
	if got.TopDelta[0].ID != "fn-11" || got.TopDelta[9].ID != "fn-02" {
		t.Fatalf("unexpected retained deltas: %+v", got.TopDelta)
	}
}

func TestCompareCyclesReportsNewAndResolved(t *testing.T) {
	base := &index.Index{Modules: map[string]*index.ModSummary{
		"a": {Name: "a", OutModules: map[string][]string{"b": nil}},
		"b": {Name: "b", OutModules: map[string][]string{"a": nil}},
	}}
	head := &index.Index{Modules: map[string]*index.ModSummary{
		"c": {Name: "c", OutModules: map[string][]string{"d": nil}},
		"d": {Name: "d", OutModules: map[string][]string{"c": nil}},
	}}
	want := Cycles{New: []string{"c -> d -> c"}, Resolved: []string{"a -> b -> a"}}
	if got := compareCycles(base, head); !reflect.DeepEqual(got, want) {
		t.Fatalf("compareCycles = %+v, want %+v", got, want)
	}
}

func TestGateTriggered(t *testing.T) {
	cases := []struct {
		name   string
		report Report
		gate   string
		want   bool
	}{
		{name: "new cycle", gate: GateCycles, report: Report{Cycles: Cycles{New: []string{"a -> a"}}}, want: true},
		{name: "resolved cycle", gate: GateCycles, report: Report{Cycles: Cycles{Resolved: []string{"a -> a"}}}},
		{name: "new dead code", gate: GateDeadCode, report: Report{Dead: DeadCode{New: []DeadItem{{ID: "dead"}}}}, want: true},
		{name: "removed API", gate: GateBreakingAPI, report: Report{API: API{Removed: []APIChange{{ID: "old"}}}}, want: true},
		{name: "modified API", gate: GateBreakingAPI, report: Report{API: API{Modified: []APIChange{{ID: "changed"}}}}, want: true},
		{name: "added API", gate: GateBreakingAPI, report: Report{API: API{Added: []APIChange{{ID: "new"}}}}},
		{name: "hotspot delta", gate: GateUntestedHotspots, report: Report{Hotspots: Hotspots{TopDelta: []Hotspot{{ID: "changed"}}}}, want: true},
		{name: "unknown gate", gate: "unknown"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := GateTriggered(test.report, test.gate); got != test.want {
				t.Fatalf("GateTriggered(%q) = %v, want %v", test.gate, got, test.want)
			}
		})
	}
}

func TestFailedGatesPreservesOrder(t *testing.T) {
	report := Report{
		Cycles: Cycles{New: []string{"a -> a"}},
		API:    API{Removed: []APIChange{{ID: "old"}}},
	}
	got := FailedGates(report, []string{GateBreakingAPI, GateDeadCode, GateCycles})
	want := []string{GateBreakingAPI, GateCycles}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FailedGates = %v, want %v", got, want)
	}
	if got := FailedGates(report, nil); got != nil {
		t.Fatalf("empty gates = %v", got)
	}
}

func TestNormalizeGatesAcceptsStringsAndArrays(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		want    []string
		wantErr string
	}{
		{name: "comma string", values: []string{"cycles, breaking_api"}, want: []string{GateCycles, GateBreakingAPI}},
		{name: "space string", values: []string{"dead_code untested_hotspots"}, want: []string{GateDeadCode, GateUntestedHotspots}},
		{name: "array with duplicates", values: []string{"cycles", "cycles", "dead_code"}, want: []string{GateCycles, GateDeadCode}},
		{name: "empty", values: nil},
		{name: "unknown", values: []string{"cycles,nope"}, wantErr: "unknown fail_on gate"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeGates(test.values)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("gates = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReportJSONFieldNamesStayStable(t *testing.T) {
	data, err := json.Marshal(Report{
		BaseRef: "HEAD",
		Summary: Counters{Packages: 2},
		Cycles:  Cycles{New: []string{"a -> b -> a"}},
		API:     API{Modified: []APIChange{{ID: "sym:x", Name: "X"}}},
		Hotspots: Hotspots{
			TopDelta: []Hotspot{{ID: "sym:y", DeltaCC: 1}},
		},
		Dead:   DeadCode{New: []DeadItem{{ID: "sym:z"}}},
		Failed: []string{GateCycles},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"base_ref"`, `"base_commit"`, `"head_ref"`, `"head"`, `"summary"`,
		`"breaking_api"`, `"hotspots"`, `"top_delta"`, `"after_complexity"`,
		`"dead_code"`, `"failed_gates"`, `"packages_base"`, `"untested_hotspots"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report JSON missing %s: %s", want, text)
		}
	}
}

package main

import (
	"bytes"
	"errors"
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

	want := diffAPI{
		Removed:  []diffAPIChange{{ID: "sym:example.com/m.Removed", Name: "Removed", Kind: "func", File: "api.go", Line: 1, Before: "func Removed()"}},
		Modified: []diffAPIChange{{ID: "sym:example.com/m.Changed", Name: "Changed", Kind: "func", File: "api.go", Line: 1, Before: "func Changed(int)", After: "func Changed(string)"}},
		Added:    []diffAPIChange{{ID: "sym:example.com/m.Added", Name: "Added", Kind: "func", File: "api.go", Line: 1, Before: "func Added()"}},
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
	wantDelta := []diffHotspot{{
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
	want := diffCycles{New: []string{"c -> d -> c"}, Resolved: []string{"a -> b -> a"}}
	if got := compareCycles(base, head); !reflect.DeepEqual(got, want) {
		t.Fatalf("compareCycles = %+v, want %+v", got, want)
	}
}

func TestGateTriggered(t *testing.T) {
	cases := []struct {
		name   string
		report diffReport
		gate   string
		want   bool
	}{
		{name: "new cycle", gate: "cycles", report: diffReport{Cycles: diffCycles{New: []string{"a -> a"}}}, want: true},
		{name: "resolved cycle", gate: "cycles", report: diffReport{Cycles: diffCycles{Resolved: []string{"a -> a"}}}},
		{name: "new dead code", gate: "dead_code", report: diffReport{Dead: diffDeadCode{New: []diffDeadItem{{ID: "dead"}}}}, want: true},
		{name: "removed API", gate: "breaking_api", report: diffReport{API: diffAPI{Removed: []diffAPIChange{{ID: "old"}}}}, want: true},
		{name: "modified API", gate: "breaking_api", report: diffReport{API: diffAPI{Modified: []diffAPIChange{{ID: "changed"}}}}, want: true},
		{name: "added API", gate: "breaking_api", report: diffReport{API: diffAPI{Added: []diffAPIChange{{ID: "new"}}}}},
		{name: "hotspot delta", gate: "untested_hotspots", report: diffReport{Hotspots: diffHotspots{TopDelta: []diffHotspot{{ID: "changed"}}}}, want: true},
		{name: "unknown gate", gate: "unknown"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := gateTriggered(test.report, test.gate); got != test.want {
				t.Fatalf("gateTriggered(%q) = %v, want %v", test.gate, got, test.want)
			}
		})
	}
}

type failingDiffWriter struct{}

func (failingDiffWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestPrintDiffMarkdownSectionsAndWriteErrors(t *testing.T) {
	report := diffReport{
		BaseRef: "main",
		Summary: diffCounters{PackagesBase: 1, Packages: 2},
		Cycles:  diffCycles{New: []string{"a -> b -> a"}},
		API:     diffAPI{Modified: []diffAPIChange{{Name: "Changed", File: "api.go", Line: 3, Before: "int", After: "string"}}},
		Hotspots: diffHotspots{
			New:      []diffHotspot{{Name: "Complex", AfterCC: 12, AfterLOC: 20, File: "complex.go", Line: 4}},
			TopDelta: []diffHotspot{{Name: "Changed", BeforeCC: 10, AfterCC: 11, BeforeLOC: 10, AfterLOC: 12, DeltaCC: 1, DeltaLOC: 2}},
		},
		Dead:   diffDeadCode{Removed: []diffDeadItem{{Name: "Gone", File: "dead.go", Line: 5}}},
		Failed: []string{"cycles"},
	}
	var out bytes.Buffer
	if err := printDiffMarkdown(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# goray diff: `main`", "## Cycles", "## Breaking API", "## Hotspots", "## Dead Code", "**Failed gates:** cycles", "ΔCC"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("markdown missing %q:\n%s", want, out.String())
		}
	}
	if err := printDiffMarkdown(failingDiffWriter{}, report); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("write error = %v", err)
	}
}

package report

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func TestBuildMetrics(t *testing.T) {
	idx := &index.Index{
		Modules:  map[string]*index.ModSummary{"a": {}, "b": {}},
		Packages: map[string]*index.PkgInfo{"a": {ProdLOC: 10}, "b": {ProdLOC: 20}},
	}
	rows := []render.HTMLSymbol{
		{Kind: "file", Dead: true},
		{Kind: "type"},
		{Kind: "func"},
		{Kind: "method"},
		{Kind: "interface", Dead: true},
	}
	want := render.HTMLMetrics{Modules: 2, Packages: 2, Files: 1, Types: 1, Funcs: 1, Methods: 1, LOC: 30, Dead: 2}
	if got := buildMetrics(idx, rows); got != want {
		t.Fatalf("buildMetrics = %+v, want %+v", got, want)
	}
}

func TestCoverageFromStats(t *testing.T) {
	tests := []struct {
		name  string
		stats cache.Stats
		want  render.HTMLCoverage
	}{
		{"empty", cache.Stats{}, render.HTMLCoverage{}},
		{
			"rounded",
			cache.Stats{
				Tables: map[string]int{"working_symbols": 8}, EligibleSymbols: 3,
				SummaryStates: map[string]int{"synced": 2, "hard_stale": 1},
			},
			render.HTMLCoverage{Eligible: 3, Synced: 2, HardStale: 1, NotEligible: 5, Percent: 66.7},
		},
		{
			"zero eligible",
			cache.Stats{
				Tables:        map[string]int{"working_symbols": 2},
				SummaryStates: map[string]int{"synced": 1},
			},
			render.HTMLCoverage{Synced: 1, NotEligible: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := coverageFromStats(tc.stats); got != tc.want {
				t.Fatalf("coverageFromStats = %+v, want %+v", got, tc.want)
			}
		})
	}
	got, err := buildCoverage(context.Background(), nil)
	if err != nil || got != (render.HTMLCoverage{}) {
		t.Fatalf("buildCoverage(nil) = %+v, %v", got, err)
	}
}

func TestBuildCouplingOrderingAndLimit(t *testing.T) {
	idx := &index.Index{
		Packages:   make(map[string]*index.PkgInfo),
		RevImports: make(map[string][]string),
	}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		fanOut := i % 3
		if i == 11 {
			fanOut = 3
		}
		idx.Packages[name] = &index.PkgInfo{Imports: make([]string, fanOut)}
		fanIn := 2 - fanOut
		if fanIn < 0 {
			fanIn = 0
		}
		idx.RevImports[name] = make([]string, fanIn)
	}
	coupled := buildCoupling(idx)
	want := []string{"pkg11", "pkg00", "pkg01", "pkg02", "pkg03", "pkg04", "pkg05", "pkg06", "pkg07", "pkg08"}
	got := make([]string, 0, len(coupled))
	for _, item := range coupled {
		got = append(got, item.Pkg)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coupling order = %v, want %v", got, want)
	}
}

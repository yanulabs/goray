package index

import "testing"

func TestHotspotRiskDownweightsTrivialHighFanIn(t *testing.T) {
	const (
		wrapperID = "example.com/issue79/pkg.wrapper.Error"
		complexID = "example.com/issue79/pkg.reconcile"
	)
	idx := &Index{
		Symbols: map[string]*Symbol{
			wrapperID: {
				ID: wrapperID, Name: "Error", Kind: "method", RecvType: "wrapper",
				File: "pkg/wrapper.go", Line: 1, LOC: 3, Cyclomatic: 1, ModuleArea: "pkg",
			},
			complexID: {
				ID: complexID, Name: "reconcile", Kind: "func",
				File: "pkg/reconcile.go", Line: 1, LOC: 90, Cyclomatic: 20, ModuleArea: "pkg",
			},
		},
		Callers: map[string][]string{wrapperID: make([]string, 143)},
	}

	items, total := idx.Hotspots(HotspotOptions{Target: "func", SortBy: "risk", Limit: 10})
	if total != 2 || len(items) != 2 {
		t.Fatalf("hotspots = %d/%d", len(items), total)
	}
	if items[0].ID != complexID {
		t.Fatalf("trivial wrapper outranked complex function: %+v", items)
	}
	for _, item := range items {
		if item.ID != wrapperID {
			continue
		}
		if item.RecvType != "wrapper" {
			t.Fatalf("hotspot receiver = %q", item.RecvType)
		}
		if item.RiskScore >= items[0].RiskScore {
			t.Fatalf("risk scores not ordered: %+v", items)
		}
	}
	if riskScore(1, 143) >= riskScore(20, 0) {
		t.Fatalf("riskScore(1, 143) = %d did not lose to riskScore(20, 0) = %d", riskScore(1, 143), riskScore(20, 0))
	}
}

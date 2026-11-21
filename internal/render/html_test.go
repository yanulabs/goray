package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func htmlPayload(t *testing.T, html string) string {
	t.Helper()
	marker := `<script id="report-data" type="application/json">`
	start := strings.Index(html, marker)
	if start < 0 {
		t.Fatal("report-data script not found")
	}
	start += len(marker)
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatal("report-data script not closed")
	}
	return html[start : start+end]
}

func emptyReport() HTMLReport {
	return HTMLReport{
		Repo: "example.com/app", ModulePath: "example.com/app", GeneratedAt: "2026-01-01T00:00:00Z",
		Metrics:  HTMLMetrics{Modules: 1, Packages: 1, Files: 1, Types: 1, Funcs: 1, Methods: 1, LOC: 42, Dead: 1},
		Coverage: HTMLCoverage{Eligible: 4, Synced: 1, SoftStale: 1, HardStale: 1, Missing: 1, NotEligible: 2, Percent: 25},
		Modules:  []HTMLModuleCard{{ID: "mod:app", Name: "app", SyncState: "missing"}},
		Packages: []HTMLPackageCard{{ID: "pkg:example.com/app", PkgPath: "example.com/app", RelPath: ".", SyncState: "missing"}},
		Symbols: []HTMLSymbol{{
			ID: "sym:example.com/app.F", Name: "F", Kind: "func", PkgPath: "example.com/app",
			File: "f.go", StartLine: 1, EndLine: 10, LOC: 10, Complexity: 3, FanIn: 2, FanOut: 1,
			Eligible: true, SyncState: "missing",
		}},
		Tree: []*HTMLTreeNode{{ID: "mod:app", Kind: "module", Label: "app"}},
		Graphs: HTMLGraphs{
			Modules:  HTMLGraph{Nodes: []HTMLGraphNode{{ID: "mod:app", Label: "app", Kind: "module"}}},
			Packages: HTMLGraph{Nodes: []HTMLGraphNode{{ID: "pkg:example.com/app", Label: ".", Kind: "package", Module: "app"}}},
		},
	}
}

func TestRenderHTMLSelfContainedAndEmptySummaries(t *testing.T) {
	html, err := RenderHTML(emptyReport())
	if err != nil {
		t.Fatal(err)
	}
	if len(html) < 1000 {
		t.Fatalf("report too small: %d", len(html))
	}
	payload := htmlPayload(t, html)
	if !json.Valid([]byte(payload)) {
		t.Fatal("embedded report-data is not valid JSON")
	}
	for _, forbidden := range []string{"http://", "https://", "@import"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("report contains external/network reference %q", forbidden)
		}
	}
	if !strings.Contains(html, "No cached LLM summary") {
		t.Fatal("empty-summary fallback is missing")
	}
}

func TestRenderHTMLEscapesScriptBreakout(t *testing.T) {
	attack := `</script><img src=x onerror=alert(1)>`
	report := emptyReport()
	report.Symbols[0].Name = attack
	report.Symbols[0].Summary = &HTMLSummary{
		Intent: attack, SideEffects: []string{"db_write"}, Invariants: []string{attack},
		SyncState: "soft_stale", DepsChanged: true,
	}
	html, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "</script><img") {
		t.Fatal("summary broke out of the report-data script")
	}
	payload := htmlPayload(t, html)
	if !strings.Contains(payload, `\u003c/script\u003e`) || !strings.Contains(payload, `\u003cimg`) {
		t.Fatalf("payload was not HTML-escaped: %s", payload)
	}
	if !json.Valid([]byte(payload)) {
		t.Fatal("escaped payload is not valid JSON")
	}
}

func TestRenderHTMLSummaries(t *testing.T) {
	report := emptyReport()
	report.Symbols = append(report.Symbols, HTMLSymbol{
		ID: "sym:example.com/app.G", Name: "G", Kind: "method", PkgPath: "example.com/app",
		LOC: 30, Complexity: 12, FanIn: 4, SyncState: "soft_stale",
		Summary: &HTMLSummary{
			Intent: "Persists records.", SideEffects: []string{"db_write", "spawns_goroutine"},
			Invariants: []string{"Caller holds mu"}, SyncState: "soft_stale",
			ModelID: "model-x", DepsChanged: true,
		},
	})
	html, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	payload := htmlPayload(t, html)
	var decoded HTMLReport
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sym := range decoded.Symbols {
		if sym.ID == "sym:example.com/app.G" {
			found = sym.Summary != nil && sym.SyncState == "soft_stale" && len(sym.Summary.SideEffects) == 2 && sym.Summary.DepsChanged
		}
	}
	if !found {
		t.Fatal("populated summary was not embedded")
	}
}

func TestLayoutGraphCyclesDeterministic(t *testing.T) {
	graph := HTMLGraph{
		Nodes: []HTMLGraphNode{
			{ID: "mod:a", Label: "a", Kind: "module"},
			{ID: "mod:b", Label: "b", Kind: "module"},
			{ID: "mod:c", Label: "c", Kind: "module"},
		},
		Edges: []HTMLGraphEdge{
			{From: "mod:a", To: "mod:b", Weight: 1},
			{From: "mod:b", To: "mod:a", Weight: 1},
			{From: "mod:b", To: "mod:c", Weight: 1},
		},
	}
	first := LayoutGraph(graph)
	second := LayoutGraph(graph)
	if len(first.Nodes) != 3 || len(first.Edges) != 3 {
		t.Fatalf("layout = %+v", first)
	}
	for i := range first.Nodes {
		if first.Nodes[i].Layer != second.Nodes[i].Layer || first.Nodes[i].Order != second.Nodes[i].Order {
			t.Fatal("layout is not deterministic")
		}
		if first.Nodes[i].Layer < 0 || first.Nodes[i].Order < 0 {
			t.Fatalf("invalid layout node: %+v", first.Nodes[i])
		}
	}

	// Reversed input order must produce the same coordinates.
	reversed := HTMLGraph{Nodes: []HTMLGraphNode{graph.Nodes[2], graph.Nodes[1], graph.Nodes[0]}, Edges: graph.Edges}
	third := LayoutGraph(reversed)
	positions := map[string][2]int{}
	for _, node := range third.Nodes {
		positions[node.ID] = [2]int{node.Layer, node.Order}
	}
	for _, node := range first.Nodes {
		if got := positions[node.ID]; got[0] != node.Layer || got[1] != node.Order {
			t.Fatalf("layout depends on input order: %s %v vs %v", node.ID, got, [2]int{node.Layer, node.Order})
		}
	}
}

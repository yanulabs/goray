package render

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func htmlPayload(t *testing.T, html string) string {
	t.Helper()
	marker := `id="goray-data"`
	start := strings.Index(html, marker)
	if start < 0 {
		t.Fatal("goray-data script not found")
	}
	start = strings.Index(html[start:], ">") + start + 1
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatal("goray-data script not closed")
	}
	return html[start : start+end]
}

func TestHTMLReportJSONContract(t *testing.T) {
	typ := reflect.TypeOf(HTMLReport{})
	keys := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	want := []string{
		"build_tags", "coupled_packages", "coverage", "generated_at", "graphs",
		"hotspots", "interfaces", "metrics", "module_cycles", "module_path",
		"module_paths", "modules", "packages", "repo", "symbols", "tree", "workspace",
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("HTMLReport JSON contract changed:\n got: %v\nwant: %v", keys, want)
	}
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
	if strings.Contains(html, "__GORAY_REPORT_DATA__") || strings.Contains(html, "__GORAY_LIVE__") {
		t.Fatal("report contains an unresolved template placeholder")
	}
	if !strings.Contains(html, `data-goray-live="false"`) {
		t.Fatal("static report should disable live refresh")
	}
	for _, pattern := range []string{
		`<script[^>]+src\s*=`,
		`<link[^>]+href\s*=`,
		`@import\s`,
		`url\(\s*["']?https?://`,
	} {
		if regexp.MustCompile(pattern).MatchString(html) {
			t.Fatalf("report contains external asset reference matching %q", pattern)
		}
	}
	if !strings.Contains(html, "No cached LLM summary") {
		t.Fatal("empty-summary fallback is missing")
	}
	if !strings.Contains(html, "goray · Architecture Report") {
		t.Fatal("goray report title is missing")
	}
	if !strings.Contains(html, "goray-theme") {
		t.Fatal("goray theme storage key is missing")
	}
}

func TestRenderHTMLLiveFlag(t *testing.T) {
	html, err := RenderHTMLLive(emptyReport())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `data-goray-live="true"`) {
		t.Fatal("live report does not enable the refresh flag")
	}
	if !json.Valid([]byte(htmlPayload(t, html))) {
		t.Fatal("live embedded report-data is not valid JSON")
	}
}

func TestRenderHTMLEscapesScriptBreakout(t *testing.T) {
	attack := `</script><img src=x onerror=alert(1)>`
	report := emptyReport()
	report.Symbols[0].Name = attack
	report.Symbols[0].Doc = `<!-- marker & value`
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
	for _, escaped := range []string{`\u003c/script\u003e`, `\u003cimg`, `\u003c!--`, `\u0026`} {
		if !strings.Contains(payload, escaped) {
			t.Fatalf("payload was not HTML-escaped (%s): %s", escaped, payload)
		}
	}
	if !json.Valid([]byte(payload)) {
		t.Fatal("escaped payload is not valid JSON")
	}
}

func TestRenderHTMLSerializesAuditFields(t *testing.T) {
	covered := true
	report := emptyReport()
	report.Symbols[0].TestCovered = &covered
	report.Symbols[0].Untracked = true
	report.Symbols[0].Signature = "func F()"
	report.Symbols[0].Doc = "F does work."
	report.Interfaces = []HTMLInterface{{
		ID: "sym:example.com/app.I", Name: "I", PkgPath: "example.com/app",
		Module: "app", Methods: 1,
		Implementations: []HTMLImpl{{ID: "sym:example.com/app.T", Name: "T", PkgPath: "example.com/app", ViaPtr: true}},
	}}
	report.ModuleCycles = [][]string{{"a", "b", "a"}}
	html, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HTMLReport
	if err := json.Unmarshal([]byte(htmlPayload(t, html)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Symbols[0].TestCovered == nil || !*decoded.Symbols[0].TestCovered {
		t.Fatal("test coverage was not serialized")
	}
	if !decoded.Symbols[0].Untracked || decoded.Symbols[0].Signature == "" || decoded.Symbols[0].Doc == "" {
		t.Fatalf("audit fields missing: %+v", decoded.Symbols[0])
	}
	if len(decoded.Interfaces) != 1 || decoded.Interfaces[0].Module != "app" {
		t.Fatalf("interface module missing: %+v", decoded.Interfaces)
	}
	if len(decoded.ModuleCycles) != 1 || len(decoded.ModuleCycles[0]) != 3 {
		t.Fatalf("module cycles missing: %+v", decoded.ModuleCycles)
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

func TestHotspotsExcludeTestAndGeneratedSymbols(t *testing.T) {
	report := emptyReport()
	report.Symbols = []HTMLSymbol{
		{ID: "sym:example.com/app.Prod", Name: "Prod", Kind: "func", Complexity: 15, FanIn: 1},
		{ID: "sym:example.com/app.TestProd", Name: "TestProd", Kind: "func", Complexity: 99, IsTest: true},
		{ID: "sym:example.com/app.Generated", Name: "Generated", Kind: "func", Complexity: 88, IsGenerated: true},
		{ID: "file:a/a_test.go", Name: "a/a_test.go", Kind: "file", Complexity: 77, IsTest: true},
	}
	html, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HTMLReport
	if err := json.Unmarshal([]byte(htmlPayload(t, html)), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Hotspots) != 1 || decoded.Hotspots[0].ID != "sym:example.com/app.Prod" {
		t.Fatalf("hotspots = %+v", decoded.Hotspots)
	}
	flags := map[string]HTMLSymbol{}
	for _, symbol := range decoded.Symbols {
		flags[symbol.ID] = symbol
	}
	if !flags["sym:example.com/app.TestProd"].IsTest || flags["sym:example.com/app.TestProd"].IsGenerated {
		t.Fatalf("test flags not serialized: %+v", flags["sym:example.com/app.TestProd"])
	}
	if !flags["sym:example.com/app.Generated"].IsGenerated || flags["sym:example.com/app.Generated"].IsTest {
		t.Fatalf("generated flags not serialized: %+v", flags["sym:example.com/app.Generated"])
	}
	if flags["sym:example.com/app.Prod"].IsTest || flags["sym:example.com/app.Prod"].IsGenerated {
		t.Fatalf("production flags wrong: %+v", flags["sym:example.com/app.Prod"])
	}
}

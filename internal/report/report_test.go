package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func writeReportModule(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.com/reporttest\n\ngo 1.23\n",
		"a/a.go": `package a

func Leaf() int {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return 1 } } } } } } } } } }
	return 0
}

func Caller() {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { _ = Leaf() } } } } } } } } } }
}
`,
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func upsertReportSummary(t *testing.T, c *cache.Cache, idx *index.Index, id, intent string, effects []string) {
	t.Helper()
	ctx := context.Background()
	sc, err := c.SummaryContext(ctx, id, idx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"intent": intent, "side_effects": effects, "invariants": []string{"Invariant for " + intent},
	})
	results, err := c.UpsertSummaries(ctx, []cache.SummaryInput{{
		SymbolID: id, Summary: raw,
		ExpectedSelfHash: sc.SelfHash, ExpectedDepHash: sc.DepHash, ModelID: "test-model",
	}})
	if err != nil || len(results) != 1 || results[0].Status != "updated" {
		t.Fatalf("upsert %s: %+v (%v)", id, results, err)
	}
}

func TestHTMLReportEmptyAndPopulated(t *testing.T) {
	dir := t.TempDir()
	writeReportModule(t, dir)
	idx, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	empty, err := Build(ctx, idx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Symbols) == 0 || empty.Metrics.Modules == 0 || empty.Metrics.Packages == 0 {
		t.Fatalf("empty report lacks static data: %+v", empty.Metrics)
	}
	if empty.Coverage.Missing == 0 || empty.Coverage.Synced != 0 {
		t.Fatalf("unexpected empty coverage: %+v", empty.Coverage)
	}
	for _, row := range empty.Symbols {
		if row.Summary != nil {
			t.Fatalf("unexpected summary in empty cache: %+v", row)
		}
	}

	leafID := "sym:example.com/reporttest/a.Leaf"
	callerID := "sym:example.com/reporttest/a.Caller"
	upsertReportSummary(t, c, idx, leafID, "Leaf persists records.", []string{"db_write"})
	upsertReportSummary(t, c, idx, callerID, "Caller invokes Leaf.", []string{"network_io"})

	files := map[string]string{
		"go.mod": "module example.com/reporttest\n\ngo 1.23\n",
		"a/a.go": `package a

func Leaf() string {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return "x" } } } } } } } } } }
	return ""
}

func Caller() {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { _ = Leaf() } } } } } } } } } }
}
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err = index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	populated, err := Build(ctx, idx, c)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]int{}
	for i, row := range populated.Symbols {
		rows[row.ID] = i
	}
	leaf := populated.Symbols[rows[leafID]]
	caller := populated.Symbols[rows[callerID]]
	if leaf.SyncState != "hard_stale" {
		t.Fatalf("leaf state = %s", leaf.SyncState)
	}
	if caller.SyncState != "soft_stale" || caller.Summary == nil || !caller.Summary.DepsChanged {
		t.Fatalf("caller state = %s summary=%+v", caller.SyncState, caller.Summary)
	}
	if len(caller.Summary.SideEffects) != 1 || caller.Summary.SideEffects[0] != "network_io" {
		t.Fatalf("caller side effects = %v", caller.Summary.SideEffects)
	}
	if len(leaf.Summary.SideEffects) != 1 || leaf.Summary.SideEffects[0] != "db_write" {
		t.Fatalf("leaf side effects = %v", leaf.Summary.SideEffects)
	}
	if populated.Coverage.HardStale == 0 || populated.Coverage.SoftStale == 0 {
		t.Fatalf("coverage lacks stale states: %+v", populated.Coverage)
	}
	if populated.Coverage.Percent < 0 || populated.Coverage.Percent > 100 {
		t.Fatalf("invalid coverage percent: %v", populated.Coverage.Percent)
	}

	html, err := render.RenderHTML(populated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `id="goray-data"`) || !strings.Contains(html, "db_write") {
		t.Fatal("HTML report missing data or summaries")
	}
}

func complexRiskFunc(name string) string {
	body := strings.Repeat("\tif true {\n", 11)
	body += "\t\treturn 1\n"
	body += strings.Repeat("\t}\n", 11)
	return "func " + name + "() int {\n" + body + "\treturn 0\n}\n"
}

func TestHTMLReportHotspotSignal(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/reporttest\n\ngo 1.23\n",
		"a/a.go": "package a\n\n" + complexRiskFunc("Prod"),
		"a/a_test.go": `package a

import "testing"

func TestHuge(t *testing.T) {
	if true {
		if true {
			if true {
				if true {
					if true {
						if true {
							if true {
								if true {
									if true {
										if true {
											if true {
												t.Log("noise")
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
`,
		"a/generated.go": "// Code generated by tool. DO NOT EDIT.\n\npackage a\n\n" + complexRiskFunc("GeneratedHuge"),
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	reportData, err := Build(context.Background(), idx, nil)
	if err != nil {
		t.Fatal(err)
	}

	var prod, testFn, generated *render.HTMLSymbol
	for i := range reportData.Symbols {
		switch reportData.Symbols[i].ID {
		case "sym:example.com/reporttest/a.Prod":
			prod = &reportData.Symbols[i]
		case "sym:example.com/reporttest/a.TestHuge":
			testFn = &reportData.Symbols[i]
		case "sym:example.com/reporttest/a.GeneratedHuge":
			generated = &reportData.Symbols[i]
		}
	}
	if prod == nil || testFn == nil || generated == nil {
		t.Fatalf("missing symbols: prod=%v test=%v generated=%v", prod, testFn, generated)
	}
	if prod.IsTest || prod.IsGenerated {
		t.Fatalf("production flags wrong: %+v", prod)
	}
	if !testFn.IsTest || testFn.IsGenerated {
		t.Fatalf("test flags wrong: %+v", testFn)
	}
	if !generated.IsGenerated || generated.IsTest {
		t.Fatalf("generated flags wrong: %+v", generated)
	}

	foundProd := false
	for _, hotspot := range reportData.Hotspots {
		if hotspot.IsTest || hotspot.IsGenerated {
			t.Fatalf("non-production hotspot: %+v", hotspot)
		}
		if hotspot.ID == prod.ID {
			foundProd = true
		}
	}
	if !foundProd {
		t.Fatalf("production hotspot missing: %+v", reportData.Hotspots)
	}
}

package cache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

const testModule = "example.com/cachetest"

func writeCacheModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	files["go.mod"] = "module " + testModule + "\n\ngo 1.23\n"
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

func openTestCache(t *testing.T, dir string) *Cache {
	t.Helper()
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func buildSync(t *testing.T, c *Cache, dir string, files map[string]string) *index.Index {
	t.Helper()
	if files != nil {
		writeCacheModule(t, dir, files)
	}
	idx, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background(), idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func testFuncSummary(intent string) map[string]any {
	return map[string]any{
		"intent":       intent,
		"side_effects": []string{"none"},
		"invariants":   []string{},
	}
}

func testArchSummary(intent string) map[string]any {
	return map[string]any{
		"intent":         intent,
		"key_invariants": []string{},
		"boundaries":     []string{},
	}
}

func upsertSummary(t *testing.T, c *Cache, idx *index.Index, id string, summary any) UpsertResult {
	t.Helper()
	ctx := context.Background()
	sc, err := c.SummaryContext(ctx, id, idx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	results, err := c.UpsertSummaries(ctx, []SummaryInput{{
		SymbolID: id, Summary: raw,
		ExpectedSelfHash: sc.SelfHash, ExpectedDepHash: sc.DepHash,
		ModelID: "test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	return results[0]
}

func TestSyncAndCoverage(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"a/a.go": `package a

type OrderID int64

func Leaf() OrderID {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}

func Caller() {
	_ = Leaf()
}

func trivial() {}
`}
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, files)

	var count int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM working_symbols").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 7 {
		t.Fatalf("working symbols = %d", count)
	}
	if err := c.db.QueryRow("SELECT COUNT(*) FROM working_edges WHERE kind='calls'").Scan(&count); err != nil || count == 0 {
		t.Fatalf("call edges = %d (%v)", count, err)
	}

	leafID := "sym:" + testModule + "/a.Leaf"
	state, err := c.StateOf(context.Background(), leafID)
	if err != nil || state != StateMissing {
		t.Fatalf("leaf state = %s (%v)", state, err)
	}
	trivialID := "sym:" + testModule + "/a.trivial"
	sc, err := c.SummaryContext(context.Background(), trivialID, idx)
	if err != nil {
		t.Fatal(err)
	}
	if sc.SyncState != StateNotEligible || sc.Source == "" || sc.Note == "" {
		t.Fatalf("trivial context = %+v", sc)
	}
	rawInvalid, _ := json.Marshal(testFuncSummary("trivial"))
	results, err := c.UpsertSummaries(context.Background(), []SummaryInput{{
		SymbolID: trivialID, Summary: rawInvalid,
		ExpectedSelfHash: sc.SelfHash, ExpectedDepHash: sc.DepHash,
	}})
	if err != nil || results[0].Status != "validation_error" {
		t.Fatalf("not eligible upsert = %+v (%v)", results, err)
	}

	result := upsertSummary(t, c, idx, leafID, testFuncSummary("leaf"))
	if result.Status != "updated" {
		t.Fatalf("upsert = %+v", result)
	}
	state, _ = c.StateOf(context.Background(), leafID)
	if state != StateSynced {
		t.Fatalf("state = %s", state)
	}
	coverage, err := c.Coverage(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Totals.Synced != 1 || coverage.Totals.Eligible == 0 {
		t.Fatalf("coverage = %+v", coverage.Totals)
	}

	stale, err := c.ListStale(context.Background(), StaleFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range stale {
		if item.ID == leafID || item.ID == trivialID {
			t.Fatalf("unexpected stale item %s", item.ID)
		}
	}
}

func TestStalenessAndRollup(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a/a.go": `package a

func Leaf() OrderID {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}

func Caller() {
	if true { if true { if true { if true { _ = Leaf() } } } }
}
`,
		"a/types.go": "package a\ntype OrderID int64\n",
	}
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, files)
	ctx := context.Background()
	leafID := "sym:" + testModule + "/a.Leaf"
	callerID := "sym:" + testModule + "/a.Caller"
	fileID := "file:a/a.go"
	pkgID := "pkg:" + testModule + "/a"
	modID := "mod:a"

	for _, item := range []struct {
		id      string
		summary any
	}{
		{leafID, testFuncSummary("leaf")},
		{callerID, testFuncSummary("caller")},
		{fileID, testArchSummary("file")},
		{pkgID, testArchSummary("package")},
		{modID, testArchSummary("module")},
	} {
		if result := upsertSummary(t, c, idx, item.id, item.summary); result.Status != "updated" {
			t.Fatalf("%s: %+v", item.id, result)
		}
	}

	files["a/a.go"] = `package a

func Leaf() OrderID {
	if false { if false { if false { if false { return 2 } } } }
	return 1
}

func Caller() {
	if true { if true { if true { if true { _ = Leaf() } } } }
}
`
	idx = buildSync(t, c, dir, files)
	states := map[string]SyncState{}
	for _, id := range []string{leafID, callerID, fileID, pkgID, modID} {
		sc, err := c.SummaryContext(ctx, id, idx)
		if err != nil {
			t.Fatal(err)
		}
		states[id] = sc.SyncState
		if sc.SyncState == StateHardStale || sc.SyncState == StateSoftStale {
			if len(sc.PreviousSummary) == 0 {
				t.Fatalf("%s missing baseline summary", id)
			}
		}
	}
	if states[leafID] != StateHardStale {
		t.Fatalf("leaf state = %s", states[leafID])
	}
	if states[callerID] != StateSynced {
		t.Fatalf("caller body edit should not cascade: %s", states[callerID])
	}
	for _, id := range []string{fileID, pkgID, modID} {
		if states[id] != StateSoftStale {
			t.Fatalf("%s state = %s", id, states[id])
		}
	}

	// Signature-affecting type change makes the direct caller soft stale.
	files["a/types.go"] = "package a\ntype OrderID string\n"
	files["a/a.go"] = `package a

func Leaf() OrderID {
	if false { if false { if false { if false { return "x" } } } }
	return ""
}

func Caller() {
	if true { if true { if true { if true { _ = Leaf() } } } }
}
`
	idx = buildSync(t, c, dir, files)
	callerCtx, err := c.SummaryContext(ctx, callerID, idx)
	if err != nil {
		t.Fatal(err)
	}
	if callerCtx.SyncState != StateSoftStale || !callerCtx.DepsChanged {
		t.Fatalf("caller should be soft stale: %+v", callerCtx)
	}
	if !slices.Contains(callerCtx.ChangedDependencies, leafID) {
		t.Fatalf("caller changed deps = %v", callerCtx.ChangedDependencies)
	}
}

func TestUpsertValidationAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"a/a.go": `package a

func Leaf() int {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}
`}
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, files)
	ctx := context.Background()
	id := "sym:" + testModule + "/a.Leaf"
	sc, err := c.SummaryContext(ctx, id, idx)
	if err != nil {
		t.Fatal(err)
	}

	bad := []string{
		`{}`,
		`{"intent":"","side_effects":["none"],"invariants":[]}`,
		`{"intent":"x","side_effects":["bogus"],"invariants":[]}`,
		`{"intent":"x","side_effects":["none","db_read"],"invariants":[]}`,
		`{"intent":"x","side_effects":["none"],"invariants":[],"extra":true}`,
	}
	for _, raw := range bad {
		results, err := c.UpsertSummaries(ctx, []SummaryInput{{
			SymbolID: id, Summary: json.RawMessage(raw),
			ExpectedSelfHash: sc.SelfHash, ExpectedDepHash: sc.DepHash,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Status != "validation_error" || results[0].Error == "" {
			t.Fatalf("raw %s: %+v", raw, results[0])
		}
	}

	results, err := c.UpsertSummaries(ctx, []SummaryInput{{
		SymbolID: id, Summary: json.RawMessage(`{"intent":"x","side_effects":["none"],"invariants":[]}`),
		ExpectedSelfHash: "wrong", ExpectedDepHash: sc.DepHash,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "hash_mismatch" {
		t.Fatalf("mismatch = %+v", results[0])
	}

	results, err = c.UpsertSummaries(ctx, []SummaryInput{{
		SymbolID: "missing", Summary: json.RawMessage(`{}`),
	}})
	if err != nil || results[0].Status != "not_found" {
		t.Fatalf("missing = %+v (%v)", results[0], err)
	}
}

func TestBranchSwitchAndRenameResilience(t *testing.T) {
	dir := t.TempDir()
	base := `package a

func Standalone() int {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}

func Keep() int { return 1 }
`
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, map[string]string{"a/a.go": base})
	ctx := context.Background()
	id := "sym:" + testModule + "/a.Standalone"
	if result := upsertSummary(t, c, idx, id, testFuncSummary("standalone")); result.Status != "updated" {
		t.Fatalf("%+v", result)
	}

	buildSync(t, c, dir, map[string]string{"a/a.go": "package a\n\nfunc Keep() int { return 1 }\n"})
	if _, err := c.StateOf(ctx, id); err == nil {
		t.Fatal("removed symbol should be absent from working state")
	}
	idx = buildSync(t, c, dir, map[string]string{"a/a.go": base})
	state, err := c.StateOf(ctx, id)
	if err != nil || state != StateSynced {
		t.Fatalf("restored state = %s (%v)", state, err)
	}
	if _, err := c.StateOf(ctx, id); err != nil {
		t.Fatal(err)
	}
	_ = idx
}

func TestGCAndTouch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"a/a.go": `package a

func Leaf() int {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}
`}
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, files)
	ctx := context.Background()
	id := "sym:" + testModule + "/a.Leaf"
	if result := upsertSummary(t, c, idx, id, testFuncSummary("leaf")); result.Status != "updated" {
		t.Fatalf("%+v", result)
	}

	var oldAccess string
	if err := c.db.QueryRow("SELECT last_accessed_at FROM summary_cache").Scan(&oldAccess); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, _, _, err := c.Enrich(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.FlushTouches(ctx); err != nil {
		t.Fatal(err)
	}
	var newAccess string
	if err := c.db.QueryRow("SELECT last_accessed_at FROM summary_cache").Scan(&newAccess); err != nil {
		t.Fatal(err)
	}
	if newAccess == oldAccess {
		t.Fatal("touch did not update last_accessed_at")
	}

	// Editing the body leaves the old content-addressed summary unreferenced.
	files["a/a.go"] = `package a

func Leaf() int {
	if false { if false { if false { if false { return 2 } } } }
	return 1
}
`
	buildSync(t, c, dir, files)
	if err := c.RunGC(ctx, time.Now().UTC().Add(time.Hour), 0, 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM summary_cache").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cache rows after GC = %d", count)
	}
	if err := c.db.QueryRow("SELECT COUNT(*) FROM symbol_history").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("history rows after GC = %d", count)
	}

	// A referenced entry survives.
	if result := upsertSummary(t, c, idx, id, testFuncSummary("replacement")); result.Status != "updated" {
		// The old index snapshot is intentionally stale; use a fresh context.
		idx2, err := index.Build(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Sync(ctx, idx2); err != nil {
			t.Fatal(err)
		}
		if result := upsertSummary(t, c, idx2, id, testFuncSummary("replacement")); result.Status != "updated" {
			t.Fatalf("%+v", result)
		}
	}
	if err := c.RunGC(ctx, time.Now().UTC().Add(2*time.Hour), 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRow("SELECT COUNT(*) FROM summary_cache").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("referenced cache rows = %d", count)
	}
}

func TestResolvePath(t *testing.T) {
	project := t.TempDir()
	t.Setenv("GO_MCP_DB", "")
	path, err := ResolvePath(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || filepath.Ext(path) != ".db" {
		t.Fatalf("default path = %q", path)
	}

	t.Setenv("GO_MCP_DB", ":memory:")
	if path, err := ResolvePath(project, ""); err != nil || path != ":memory:" {
		t.Fatalf("env path = %q (%v)", path, err)
	}
	if path, err := ResolvePath(project, "/tmp/custom.db"); err != nil || path != "/tmp/custom.db" {
		t.Fatalf("flag path = %q (%v)", path, err)
	}
}

func TestSchemaPragmas(t *testing.T) {
	c := openTestCache(t, t.TempDir())
	for _, tc := range []struct {
		pragma string
		want   int
	}{
		{"PRAGMA foreign_keys", 1},
		{"PRAGMA busy_timeout", 5000},
		{"PRAGMA auto_vacuum", 2},
	} {
		var got int
		if err := c.db.QueryRow(tc.pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.pragma, got, tc.want)
		}
	}
	for _, table := range []string{"working_symbols", "working_edges", "summary_cache", "symbol_history"} {
		var name string
		if err := c.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}

func TestRenameAndMoveKeepsCacheHit(t *testing.T) {
	dir := t.TempDir()
	body := `func Standalone() int {
	if true { if true { if true { if true { return 1 } } } }
	return 0
}
`
	c := openTestCache(t, dir)
	idx := buildSync(t, c, dir, map[string]string{"a/a.go": "package a\n\n" + body})
	ctx := context.Background()
	id := "sym:" + testModule + "/a.Standalone"
	if result := upsertSummary(t, c, idx, id, testFuncSummary("standalone")); result.Status != "updated" {
		t.Fatalf("%+v", result)
	}

	buildSync(t, c, dir, map[string]string{
		"a/a.go": "package a\n\nfunc Keep() int { return 1 }\n",
		"b/b.go": "package a\n\n" + strings.Replace(body, "Standalone", "Renamed", 1),
	})
	newID := "sym:" + testModule + "/b.Renamed"
	state, err := c.StateOf(ctx, newID)
	if err != nil || state != StateSynced {
		t.Fatalf("renamed state = %s (%v)", state, err)
	}
}

func TestFilePragmasPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var autoVacuum int
	var journal string
	if err := c.db.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 2 || journal != "wal" {
		t.Fatalf("auto_vacuum=%d journal=%s", autoVacuum, journal)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.db.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 2 {
		t.Fatalf("auto_vacuum after reopen = %d", autoVacuum)
	}
}

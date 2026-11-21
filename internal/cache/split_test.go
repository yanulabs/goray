package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yanulabs/goray/internal/index"
)

func bigFunc(name, result string) string {
	return "func " + name + "() " + result + ` {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return 0 } } } } } } } } } }
	return 0
}
`
}

func bigCaller(name string) string {
	return "func " + name + `() {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { _ = Work() } } } } } } } } } }
	_ = Work()
}
`
}

func writeWorktreeModule(t *testing.T, dir, result string) {
	t.Helper()
	stable := strings.ReplaceAll(bigFunc("Stable", "int"), "return 0", "return 1")
	writeCacheModule(t, dir, map[string]string{
		"a/a.go": "package a\n\n" + bigFunc("Work", result) + "\n" + bigCaller("Caller") + "\n" + stable,
	})
}

func summaryIntent(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var payload struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Intent
}

func TestOpenSplitKeepsSchemasSeparate(t *testing.T) {
	dir := t.TempDir()
	workPath := filepath.Join(dir, "working.db")
	summaryPath := filepath.Join(dir, "summary.db")
	store, err := OpenSplit(workPath, summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	tables := func(path string) map[string]bool {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			out[name] = true
		}
		return out
	}

	work := tables(workPath)
	for _, name := range []string{"working_symbols", "working_edges", "schema_meta"} {
		if !work[name] {
			t.Fatalf("working DB missing %s: %+v", name, work)
		}
	}
	for _, name := range []string{"summary_cache", "symbol_history"} {
		if work[name] {
			t.Fatalf("working DB contains summary table %s", name)
		}
	}
	summary := tables(summaryPath)
	for _, name := range []string{"summary_cache", "symbol_history", "schema_meta"} {
		if !summary[name] {
			t.Fatalf("summary DB missing %s: %+v", name, summary)
		}
	}
	for _, name := range []string{"working_symbols", "working_edges"} {
		if summary[name] {
			t.Fatalf("summary DB contains working table %s", name)
		}
	}
}

func TestSplitPragmasAndAttachment(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenSplit(filepath.Join(dir, "working.db"), filepath.Join(dir, "summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"PRAGMA main.journal_mode", "wal"},
		{"PRAGMA summaries.journal_mode", "wal"},
		{"PRAGMA main.busy_timeout", "5000"},
		{"PRAGMA summaries.busy_timeout", "5000"},
		{"PRAGMA main.foreign_keys", "1"},
		{"PRAGMA summaries.foreign_keys", "1"},
		{"PRAGMA main.synchronous", "1"},
		{"PRAGMA summaries.synchronous", "1"},
		{"SELECT COUNT(*) FROM pragma_database_list WHERE name = 'summaries'", "1"},
	} {
		var got string
		if err := store.db.QueryRow(tc.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestWorktreeInheritanceAndBranchCoexistence(t *testing.T) {
	dir := t.TempDir()
	worktreeA := filepath.Join(dir, "a")
	worktreeB := filepath.Join(dir, "b")
	writeWorktreeModule(t, worktreeA, "int")
	writeWorktreeModule(t, worktreeB, "int64")

	summaryPath := filepath.Join(t.TempDir(), "shared-summary.db")
	ctx := context.Background()

	storeA, err := OpenSplit(filepath.Join(t.TempDir(), "a.db"), summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	idxA, err := index.Build(worktreeA)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.Sync(ctx, idxA); err != nil {
		t.Fatal(err)
	}
	workID := "sym:example.com/cachetest/a.Work"
	callerID := "sym:example.com/cachetest/a.Caller"
	stableID := "sym:example.com/cachetest/a.Stable"
	for id, intent := range map[string]string{
		workID:   "original-work",
		callerID: "original-caller",
		stableID: "original-stable",
	} {
		if result := upsertSummary(t, storeA, idxA, id, testFuncSummary(intent)); result.Status != "updated" {
			t.Fatalf("upsert %s: %+v", id, result)
		}
	}

	storeB, err := OpenSplit(filepath.Join(t.TempDir(), "b.db"), summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	idxB, err := index.Build(worktreeB)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeB.Sync(ctx, idxB); err != nil {
		t.Fatal(err)
	}

	var cached int
	if err := storeB.db.QueryRow("SELECT COUNT(*) FROM summaries.summary_cache").Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != 3 {
		t.Fatalf("sync triggered summary writes: %d rows", cached)
	}
	stableState, err := storeB.StateOf(ctx, stableID)
	if err != nil || stableState != StateSynced {
		t.Fatalf("unmodified state = %s (%v), want synced", stableState, err)
	}
	stableRaw, _, _, err := storeB.Enrich(ctx, stableID)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryIntent(t, stableRaw); got != "original-stable" {
		t.Fatalf("unmodified summary = %q", got)
	}

	workState, err := storeB.StateOf(ctx, workID)
	if err != nil || workState != StateHardStale {
		t.Fatalf("modified state = %s (%v), want hard_stale", workState, err)
	}
	callerState, err := storeB.StateOf(ctx, callerID)
	if err != nil || callerState != StateSoftStale {
		t.Fatalf("caller state = %s (%v), want soft_stale", callerState, err)
	}
	stale, err := storeB.ListStale(ctx, StaleFilter{Kind: "func", State: StateHardStale})
	if err != nil {
		t.Fatal(err)
	}
	foundWork := false
	for _, item := range stale {
		if item.ID == workID {
			foundWork = true
		}
	}
	if !foundWork {
		t.Fatalf("list_stale_summaries missing %s: %+v", workID, stale)
	}

	workContext, err := storeB.SummaryContext(ctx, workID, idxB)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryIntent(t, workContext.PreviousSummary); got != "original-work" {
		t.Fatalf("previous work summary = %q", got)
	}
	callerContext, err := storeB.SummaryContext(ctx, callerID, idxB)
	if err != nil {
		t.Fatal(err)
	}
	if !callerContext.DepsChanged {
		t.Fatal("caller should report changed dependencies")
	}
	if got := summaryIntent(t, callerContext.PreviousSummary); got != "original-caller" {
		t.Fatalf("previous caller summary = %q", got)
	}

	if result := upsertSummary(t, storeB, idxB, workID, testFuncSummary("new-work")); result.Status != "updated" {
		t.Fatalf("upsert new work: %+v", result)
	}

	state, err := storeA.StateOf(ctx, workID)
	if err != nil || state != StateSynced {
		t.Fatalf("worktree A state after B upsert = %s (%v), want synced", state, err)
	}
	raw, state, _, err := storeA.Enrich(ctx, workID)
	if err != nil || state != StateSynced {
		t.Fatalf("worktree A enrich = %s (%v)", state, err)
	}
	if got := summaryIntent(t, raw); got != "original-work" {
		t.Fatalf("worktree A summary = %q, want original-work", got)
	}
	if err := storeA.db.QueryRow("SELECT COUNT(*) FROM summaries.summary_cache").Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != 4 {
		t.Fatalf("coexisting summary rows = %d, want 4", cached)
	}
}

func TestConcurrentWorktreeAccess(t *testing.T) {
	dir := t.TempDir()
	worktreeA := filepath.Join(dir, "a")
	worktreeB := filepath.Join(dir, "b")
	writeWorktreeModule(t, worktreeA, "int")
	writeWorktreeModule(t, worktreeB, "int")
	summaryPath := filepath.Join(t.TempDir(), "shared-summary.db")

	storeA, err := OpenSplit(filepath.Join(t.TempDir(), "a.db"), summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := OpenSplit(filepath.Join(t.TempDir(), "b.db"), summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()

	idxA, err := index.Build(worktreeA)
	if err != nil {
		t.Fatal(err)
	}
	idxB, err := index.Build(worktreeB)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := storeA.Sync(ctx, idxA); err != nil {
		t.Fatal(err)
	}
	if err := storeB.Sync(ctx, idxB); err != nil {
		t.Fatal(err)
	}

	workID := "sym:example.com/cachetest/a.Work"
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < 4; i++ {
		store, idx := storeA, idxA
		if i%2 == 1 {
			store, idx = storeB, idxB
		}
		run(func() error {
			for n := 0; n < 5; n++ {
				if err := store.Sync(ctx, idx); err != nil {
					return fmt.Errorf("sync: %w", err)
				}
			}
			return nil
		})
		run(func() error {
			for n := 0; n < 20; n++ {
				if _, err := store.StateOf(ctx, workID); err != nil {
					return fmt.Errorf("state: %w", err)
				}
				if _, err := store.Coverage(ctx, ""); err != nil {
					return fmt.Errorf("coverage: %w", err)
				}
				if err := store.FlushTouches(ctx); err != nil {
					return fmt.Errorf("flush: %w", err)
				}
			}
			return nil
		})
		run(func() error {
			for n := 0; n < 5; n++ {
				sc, err := store.SummaryContext(ctx, workID, idx)
				if err != nil {
					return fmt.Errorf("context: %w", err)
				}
				raw, err := json.Marshal(testFuncSummary(fmt.Sprintf("summary-%d", n)))
				if err != nil {
					return err
				}
				results, err := store.UpsertSummaries(ctx, []SummaryInput{{
					SymbolID: workID, Summary: raw,
					ExpectedSelfHash: sc.SelfHash, ExpectedDepHash: sc.DepHash,
					ModelID: "test",
				}})
				if err != nil {
					return fmt.Errorf("upsert: %w", err)
				}
				if len(results) != 1 {
					return fmt.Errorf("upsert results = %d", len(results))
				}
			}
			return nil
		})
	}
	wg.Wait()
	close(errs)
	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		t.Fatalf("concurrent access failed:\n%s", strings.Join(failures, "\n"))
	}
}

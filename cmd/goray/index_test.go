package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	indexpkg "github.com/yanulabs/goray/internal/index"
)

func indexJSON(t *testing.T, args ...string) indexResult {
	t.Helper()
	code, stdout, stderr := runDirect(t, args...)
	if code != 0 {
		t.Fatalf("index %v: code=%d stderr=%q", args, code, stderr)
	}
	var result indexResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("index JSON: %v\n%s", err, stdout)
	}
	return result
}

func canonicalCLIPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func dbStats(t *testing.T, dbPath, root string) cache.Stats {
	t.Helper()
	code, stdout, stderr := runDirect(t, "db", "info", root, "--db", dbPath)
	if code != 0 {
		t.Fatalf("db info: code=%d stderr=%q", code, stderr)
	}
	var stats cache.Stats
	if err := json.Unmarshal([]byte(stdout), &stats); err != nil {
		t.Fatalf("db info JSON: %v\n%s", err, stdout)
	}
	return stats
}

func TestIndexCreatesDatabase(t *testing.T) {
	isolateCLIEnv(t)
	root, _, file := newTestModule(t)
	// Add an intra-module import so working_edges is non-empty.
	writeTestFile(t, file, "package cache\n\nimport \"example.com/check/rootpkg\"\n\nfunc Leaf() int { return rootpkg.Root() }\n")
	dbPath := filepath.Join(t.TempDir(), "index.db")

	code, stdout, stderr := runDirect(t, "index", root, "--db", dbPath)
	if code != 0 {
		t.Fatalf("index: code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{"Indexed example.com/check", "Root:", "Database:", "Symbols:", "Edges:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("index output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("stale SQLite sidecar %s%s", dbPath, suffix)
		}
	}

	stats := dbStats(t, dbPath, root)
	if stats.Tables["working_symbols"] == 0 || stats.Tables["working_edges"] == 0 {
		t.Fatalf("working tables not populated: %+v", stats.Tables)
	}
}

func TestIndexIdempotentPreservesSummaries(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")

	code, _, stderr := runDirect(t, "index", root, "--db", dbPath)
	if code != 0 {
		t.Fatalf("initial index: code=%d stderr=%q", code, stderr)
	}
	ctx := context.Background()
	idx, err := indexpkg.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sync(ctx, idx); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	pkgID := "pkg:example.com/check/rootpkg"
	summaryContext, err := store.SummaryContext(ctx, pkgID, idx)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"intent": "root package", "key_invariants": []string{}, "boundaries": []string{"module API"},
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	results, err := store.UpsertSummaries(ctx, []cache.SummaryInput{{
		SymbolID: pkgID, Summary: raw,
		ExpectedSelfHash: summaryContext.SelfHash, ExpectedDepHash: summaryContext.DepHash,
		ModelID: "test-model",
	}})
	if err != nil || len(results) != 1 || results[0].Status != "updated" {
		_ = store.Close()
		t.Fatalf("upsert: %+v (%v)", results, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := dbStats(t, dbPath, root)

	for i := 0; i < 2; i++ {
		if code, _, stderr := runDirect(t, "index", root, "--db", dbPath); code != 0 {
			t.Fatalf("index run %d: code=%d stderr=%q", i+2, code, stderr)
		}
	}
	after := dbStats(t, dbPath, root)
	if after.Tables["summary_cache"] != 1 || after.CachedSummaries != 1 {
		t.Fatalf("summary cache changed: before=%+v after=%+v", before.Tables, after.Tables)
	}
	if after.Tables["working_symbols"] != before.Tables["working_symbols"] ||
		after.Tables["working_edges"] != before.Tables["working_edges"] {
		t.Fatalf("working state changed: before=%+v after=%+v", before.Tables, after.Tables)
	}
}

func TestIndexFlagOrderingAndSubdirectory(t *testing.T) {
	isolateCLIEnv(t)
	root, deep, _ := newTestModule(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")

	before := indexJSON(t, "index", "--json", "--db", dbPath, deep)
	after := indexJSON(t, "index", deep, "--json", "--db", dbPath)
	if before.Root != after.Root || before.ModulePath != after.ModulePath ||
		before.Packages != after.Packages || before.Symbols != after.Symbols ||
		before.Edges != after.Edges {
		t.Fatalf("flag order/subdirectory changed result:\nbefore=%+v\nafter=%+v", before, after)
	}
	if before.Root != canonicalCLIPath(t, root) || before.ModulePath != "example.com/check" {
		t.Fatalf("root resolution = %+v", before)
	}
}

func TestIndexJSON(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")

	code, stdout, stderr := runDirect(t, "index", root, "--json", "--db", dbPath)
	if code != 0 {
		t.Fatalf("index --json: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("index --json stderr = %q", stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"root", "module_path", "db_path", "summary_db_path", "packages", "files", "symbols",
		"edges", "eligible", "synced", "coverage_percent", "warnings", "duration_ms",
	} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("JSON missing %q: %s", field, stdout)
		}
	}
	result := indexJSON(t, "index", root, "--json", "--db", dbPath)
	if result.Root != canonicalCLIPath(t, root) || result.ModulePath != "example.com/check" || result.DBPath != dbPath {
		t.Fatalf("result = %+v", result)
	}
	if result.SummaryDBPath != dbPath {
		t.Fatalf("explicit DB summary_db_path = %q, want %q", result.SummaryDBPath, dbPath)
	}
	if result.Packages == 0 || result.Symbols == 0 || result.Eligible == 0 {
		t.Fatalf("unexpected counts: %+v", result)
	}
}

func TestIndexDefaultSplitLayout(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)

	result := indexJSON(t, "index", "--json", root)
	if result.DBPath == result.SummaryDBPath {
		t.Fatalf("default mode used one DB: %q", result.DBPath)
	}
	if filepath.Base(filepath.Dir(result.DBPath)) != "worktrees" {
		t.Fatalf("working DB path = %q", result.DBPath)
	}
	if filepath.Base(filepath.Dir(result.SummaryDBPath)) != "summaries" {
		t.Fatalf("summary DB path = %q", result.SummaryDBPath)
	}
	for _, path := range []string{result.DBPath, result.SummaryDBPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("index did not create %q: %v", path, err)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
				t.Fatalf("stale SQLite sidecar %s%s", path, suffix)
			}
		}
	}
}

func TestIndexProductionErrorPreservesSnapshot(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")
	if code, _, stderr := runDirect(t, "index", root, "--db", dbPath); code != 0 {
		t.Fatalf("initial index: code=%d stderr=%q", code, stderr)
	}
	before := dbStats(t, dbPath, root)

	writeTestFile(t, filepath.Join(root, "rootpkg", "root.go"), "package rootpkg\n\nfunc Broken( {\n")
	code, stdout, stderr := runDirect(t, "index", root, "--db", dbPath)
	if code != 1 {
		t.Fatalf("broken production index: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("broken production index stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "package load errors") {
		t.Fatalf("stderr = %q", stderr)
	}
	after := dbStats(t, dbPath, root)
	if after.Tables["working_symbols"] != before.Tables["working_symbols"] ||
		after.Tables["working_edges"] != before.Tables["working_edges"] {
		t.Fatalf("healthy snapshot changed:\nbefore=%+v\nafter=%+v", before.Tables, after.Tables)
	}
}

func TestIndexTestOnlyErrorIsWarning(t *testing.T) {
	isolateCLIEnv(t)
	root := filepath.Join(t.TempDir(), "repo")
	writeTestModule(t, root, "example.com/warn")
	writeTestFile(t, filepath.Join(root, "a", "a.go"), "package a\n\nfunc A() int { return 1 }\n")
	writeTestFile(t, filepath.Join(root, "a", "a_test.go"), "package a\n\nfunc BrokenTest( {\n")
	dbPath := filepath.Join(t.TempDir(), "index.db")

	code, stdout, stderr := runDirect(t, "index", root, "--db", dbPath)
	if code != 0 {
		t.Fatalf("test-only error index: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Fatalf("stderr missing warning: %q", stderr)
	}
	if !strings.Contains(stdout, "Warnings:") {
		t.Fatalf("stdout missing warning count:\n%s", stdout)
	}
	stats := dbStats(t, dbPath, root)
	if stats.Tables["working_symbols"] == 0 {
		t.Fatalf("production symbols not synced: %+v", stats.Tables)
	}
}

func TestIndexHelp(t *testing.T) {
	for _, args := range [][]string{{"index", "-h"}, {"index", "--help"}, {"help", "index"}} {
		code, stdout, stderr := runDirect(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		for _, want := range []string{"goray index", "--json", "Examples:"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v help missing %q:\n%s", args, want, stdout)
			}
		}
	}
	if code, _, _ := runDirect(t, "index", ".", "extra"); code != 2 {
		t.Fatalf("extra index path code = %d, want 2", code)
	}
}

func TestIndexMemoryAndEnvDB(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)

	memory := indexJSON(t, "index", "--db", ":memory:", "--json", root)
	if memory.DBPath != ":memory:" {
		t.Fatalf("memory db_path = %q", memory.DBPath)
	}
	t.Setenv("GORAY_DB", ":memory:")
	fromEnv := indexJSON(t, "index", "--json", root)
	if fromEnv.DBPath != ":memory:" {
		t.Fatalf("env db_path = %q", fromEnv.DBPath)
	}
}

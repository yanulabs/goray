package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeIncrementalModule(t *testing.T) (root, leafFile string) {
	t.Helper()
	root = t.TempDir()
	writeTagModule(t, root, "example.com/inc", map[string]string{
		"leaf/leaf.go": `package leaf

func Value() int { return 1 }
`,
		"mid/mid.go": `package mid

import "example.com/inc/leaf"

func Use() int { return leaf.Value() + 1 }
`,
		"sibling/sibling.go": `package sibling

func Alone() int { return 7 }
`,
		"sibling2/sibling.go": `package sibling2

func Alone() int { return 8 }
`,
		"sibling3/sibling.go": `package sibling3

func Alone() int { return 9 }
`,
		"sibling4/sibling.go": `package sibling4

func Alone() int { return 10 }
`,
		"sibling5/sibling.go": `package sibling5

func Alone() int { return 11 }
`,
		"cmd/app/main.go": `package main

import "example.com/inc/mid"

func main() { _ = mid.Use() }
`,
		"leaf/leaf_test.go": `package leaf

import "testing"

func TestValue(t *testing.T) {
	if Value() != 1 {
		t.Fatal("bad")
	}
}
`,
	})
	return root, filepath.Join(root, "leaf", "leaf.go")
}

func assertSnapshotsEqual(t *testing.T, want, got *Index) {
	t.Helper()
	if !reflect.DeepEqual(sortedKeys(want.Symbols), sortedKeys(got.Symbols)) {
		t.Fatalf("symbol sets differ: want %d got %d", len(want.Symbols), len(got.Symbols))
	}
	for id, wantSym := range want.Symbols {
		gotSym := got.Symbols[id]
		if gotSym == nil {
			t.Fatalf("missing symbol %s", id)
		}
		if wantSym.Kind != gotSym.Kind || wantSym.File != gotSym.File || wantSym.Line != gotSym.Line ||
			wantSym.Signature != gotSym.Signature || wantSym.IsTest != gotSym.IsTest {
			t.Fatalf("symbol mismatch %s: %+v != %+v", id, wantSym, gotSym)
		}
	}
	if !reflect.DeepEqual(want.XRefs, got.XRefs) {
		t.Fatalf("xrefs differ")
	}
	if !reflect.DeepEqual(want.Callers, got.Callers) || !reflect.DeepEqual(want.Callees, got.Callees) {
		t.Fatalf("call graph differs")
	}
	if !reflect.DeepEqual(want.CallEdgeKinds, got.CallEdgeKinds) {
		t.Fatalf("call edge kinds differ")
	}
	if !reflect.DeepEqual(want.TestCovered, got.TestCovered) {
		t.Fatalf("test coverage differs")
	}
	if !reflect.DeepEqual(want.IfaceImpls, got.IfaceImpls) || !reflect.DeepEqual(want.TypeImpls, got.TypeImpls) {
		t.Fatalf("implementation matrix differs")
	}
	if !reflect.DeepEqual(want.CompositeLiterals, got.CompositeLiterals) {
		t.Fatalf("composite literals differ")
	}
	if !reflect.DeepEqual(want.ImportSites, got.ImportSites) {
		t.Fatalf("import sites differ")
	}
	for id, wantSummary := range want.Summary.ByID {
		gotSummary := got.Summary.ByID[id]
		if gotSummary == nil {
			t.Fatalf("missing summary %s", id)
		}
		if wantSummary.SelfHash != gotSummary.SelfHash || wantSummary.DepHash != gotSummary.DepHash ||
			wantSummary.ContentHash != gotSummary.ContentHash || wantSummary.SigHash != gotSummary.SigHash {
			t.Fatalf("summary hash mismatch %s: %+v != %+v", id, wantSummary, gotSummary)
		}
	}
	if !reflect.DeepEqual(want.TestLoadErrors, got.TestLoadErrors) {
		t.Fatalf("test load errors differ: %v != %v", want.TestLoadErrors, got.TestLoadErrors)
	}
}

func TestIncrementalEquivalence(t *testing.T) {
	root, leafFile := writeIncrementalModule(t)
	prev, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leafFile, []byte("package leaf\n\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var loaded []string
	incrementalLoadHook = func(paths []string) { loaded = append([]string{}, paths...) }
	defer func() { incrementalLoadHook = nil }()

	incremental, usedIncremental, err := BuildIncremental(root, BuildOptions{}, prev, []string{leafFile})
	if err != nil {
		t.Fatal(err)
	}
	if !usedIncremental {
		t.Fatal("expected an incremental rebuild")
	}
	sort.Strings(loaded)
	wantLoaded := []string{"example.com/inc/cmd/app", "example.com/inc/leaf", "example.com/inc/mid"}
	if !reflect.DeepEqual(loaded, wantLoaded) {
		t.Fatalf("loaded packages = %v, want %v", loaded, wantLoaded)
	}

	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, full, incremental)
	if incremental.Symbols["example.com/inc/leaf.Value"].Line == 0 {
		t.Fatal("leaf symbol missing after incremental rebuild")
	}
}

func TestIncrementalFallsBackForModuleAndPackageChanges(t *testing.T) {
	root, leafFile := writeIncrementalModule(t)
	prev, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/inc\n\ngo 1.23\n\n// touched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, used, err := BuildIncremental(root, BuildOptions{}, prev, []string{filepath.Join(root, "go.mod")}); err != nil || used {
		t.Fatalf("go.mod change: used=%v err=%v", used, err)
	}

	fresh, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "newpkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(root, "newpkg", "new.go")
	if err := os.WriteFile(newFile, []byte("package newpkg\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, used, err := BuildIncremental(root, BuildOptions{}, fresh, []string{newFile}); err != nil || used {
		t.Fatalf("new package: used=%v err=%v", used, err)
	}
	_ = leafFile
}

func TestIncrementalSyntaxErrorKeepsStoreHealthy(t *testing.T) {
	root, leafFile := writeIncrementalModule(t)
	store := NewStore(root, nil)
	store.Start()
	before, err := store.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leafFile, []byte("package leaf\n\nfunc Value() int { return 1 \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = store.Rebuild()
	if err == nil {
		t.Fatal("expected a rebuild error for a syntax error")
	}
	store.MarkDirty(leafFile)
	if err := store.rebuild(true); err == nil {
		t.Fatal("expected an incremental rebuild error")
	}
	after, err := store.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("store replaced the healthy snapshot after a failed rebuild")
	}
	if store.Status().IndexState != StateError {
		t.Fatalf("state = %s", store.Status().IndexState)
	}

	if err := os.WriteFile(leafFile, []byte("package leaf\n\nfunc Value() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty(leafFile)
	if err := store.rebuild(true); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Symbols["example.com/inc/leaf.Value"] == nil {
		t.Fatal("index did not recover after fixing the syntax error")
	}
	if status := store.Status(); status.LastBuildMode != "incremental" {
		t.Fatalf("last_build_mode = %q, want incremental", status.LastBuildMode)
	}
	if err := store.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if status := store.Status(); status.LastBuildMode != "full" {
		t.Fatalf("reindex last_build_mode = %q, want full", status.LastBuildMode)
	}
}

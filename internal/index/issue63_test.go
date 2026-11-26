package index

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func storeRequeueModule(t *testing.T) (root, aFile, bFile string) {
	t.Helper()
	root = t.TempDir()
	writeTagModule(t, root, "example.com/requeue", map[string]string{
		"a/a.go":  "package a\n\nfunc Value() int { return 1 }\n",
		"b/b.go":  "package b\n\nfunc Value() int { return 2 }\n",
		"s1/s.go": "package s1\n\nfunc Value() int { return 3 }\n",
		"s2/s.go": "package s2\n\nfunc Value() int { return 4 }\n",
		"s3/s.go": "package s3\n\nfunc Value() int { return 5 }\n",
		"s4/s.go": "package s4\n\nfunc Value() int { return 6 }\n",
	})
	return root, filepath.Join(root, "a", "a.go"), filepath.Join(root, "b", "b.go")
}

func TestStoreRequeuesFailedPendingChanges(t *testing.T) {
	root, aFile, bFile := storeRequeueModule(t)
	store := NewStore(root, nil)
	store.Start()
	if _, err := store.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(aFile, []byte("package a\n\nfunc Value() int { return 10 }\n\nfunc Extra() int { return 11 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bFile, []byte("package b\n\nfunc Value() int { return 2 \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty(aFile, bFile)
	if err := store.rebuild(context.Background(), true); err == nil {
		t.Fatal("expected the broken package to fail the rebuild")
	}
	if state := store.State(); state != StateError {
		t.Fatalf("state after failed build = %s", state)
	}

	if err := os.WriteFile(bFile, []byte("package b\n\nfunc Value() int { return 20 }\n\nfunc Extra() int { return 21 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty(bFile)
	if err := store.rebuild(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	idx, err := store.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if idx.Symbols["example.com/requeue/a.Extra"] == nil {
		t.Fatalf("valid edit from the failed batch was lost: %v", sortedKeys(idx.Symbols))
	}
	if idx.Symbols["example.com/requeue/b.Extra"] == nil {
		t.Fatal("fixed package was not reloaded")
	}
	if status := store.Status(); status.LastBuildMode != "incremental" {
		t.Fatalf("recovery mode = %q, want incremental", status.LastBuildMode)
	}
}

func TestStoreMarkDirtyWithoutPathsForcesFullRebuild(t *testing.T) {
	root, aFile, _ := storeRequeueModule(t)
	store := NewStore(root, nil)
	store.Start()
	if _, err := store.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(aFile, []byte("package a\n\nfunc Value() int { return 12 }\n\nfunc Extra() int { return 13 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty(aFile)
	store.MarkDirty()
	if err := store.rebuild(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	idx, err := store.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if idx.Symbols["example.com/requeue/a.Extra"] == nil {
		t.Fatal("full rebuild did not observe the queued edit")
	}
	if status := store.Status(); status.LastBuildMode != "full" {
		t.Fatalf("mode = %q, want full", status.LastBuildMode)
	}
}

func TestStoreReportsActualIncrementalReloadCount(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/count", map[string]string{
		"leaf/leaf.go": "package leaf\n\nfunc Value() int { return 1 }\n",
		"mid/mid.go": `package mid

import "example.com/count/leaf"

func Use() int { return leaf.Value() + 1 }
`,
		"app/app.go": `package app

import "example.com/count/mid"

func Run() int { return mid.Use() }
`,
		"s1/s.go": "package s1\n\nfunc Value() int { return 1 }\n",
		"s2/s.go": "package s2\n\nfunc Value() int { return 2 }\n",
		"s3/s.go": "package s3\n\nfunc Value() int { return 3 }\n",
	})
	store := NewStore(root, nil)
	store.Start()
	if _, err := store.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	leafFile := filepath.Join(root, "leaf", "leaf.go")
	if err := os.WriteFile(leafFile, []byte("package leaf\n\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty(leafFile)
	if err := store.rebuild(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if status := store.Status(); status.LastBuildMode != "incremental" || status.AffectedPackages != 3 {
		t.Fatalf("status = mode %q affected %d, want incremental/3", status.LastBuildMode, status.AffectedPackages)
	}
}

func TestIncrementalReloadsTestOnlyImporters(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/testclosure", map[string]string{
		"leaf/leaf.go": "package leaf\n\nfunc Value() int { return 1 }\n",
		"mid/mid.go":   "package mid\n\nfunc Local() int { return 2 }\n",
		"mid/mid_test.go": `package mid

import (
	"testing"

	"example.com/testclosure/leaf"
)

func TestLeaf(t *testing.T) { _ = leaf.Value() }
`,
		"mid/external_test.go": `package mid_test

import (
	"testing"

	"example.com/testclosure/leaf"
)

func TestExternalLeaf(t *testing.T) { _ = leaf.Value() }
`,
		"s1/s.go": "package s1\n\nfunc Value() int { return 1 }\n",
		"s2/s.go": "package s2\n\nfunc Value() int { return 2 }\n",
		"s3/s.go": "package s3\n\nfunc Value() int { return 3 }\n",
	})
	prev, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if !prev.TestCovered["example.com/testclosure/leaf.Value"] {
		t.Fatal("fixture test importer did not seed coverage")
	}
	leafFile := filepath.Join(root, "leaf", "leaf.go")
	if err := os.WriteFile(leafFile, []byte("package leaf\n\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var loaded []string
	incrementalLoadHook = func(paths []string) { loaded = append([]string{}, paths...) }
	defer func() { incrementalLoadHook = nil }()

	next, used, count, err := BuildIncremental(root, BuildOptions{}, prev, []string{leafFile})
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("expected an incremental rebuild")
	}
	if !slices.Contains(loaded, "example.com/testclosure/mid") {
		t.Fatalf("test-only importer was not reloaded: %v", loaded)
	}
	if count != len(loaded) {
		t.Fatalf("reported reload count %d, loaded %v", count, loaded)
	}
	if !next.TestCovered["example.com/testclosure/leaf.Value"] {
		t.Fatal("test coverage lost after incremental rebuild")
	}
}

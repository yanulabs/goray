package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRootGoMod(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestResolveModuleRootFromSubdirectoryAndFile(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root)
	deep := filepath.Join(root, "internal", "cache")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(deep, "summary.go")
	if err := os.WriteFile(file, []byte("package cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := canonicalTestPath(t, root)
	for _, input := range []string{root, deep, file} {
		got, err := ResolveModuleRoot(input)
		if err != nil {
			t.Fatalf("ResolveModuleRoot(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ResolveModuleRoot(%q) = %q, want %q", input, got, want)
		}
	}

	t.Chdir(deep)
	got, err := ResolveModuleRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("default root = %q, want %q", got, want)
	}
}

func TestResolveModuleRootSymlinks(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	writeRootGoMod(t, realRoot)
	deep := filepath.Join(realRoot, "pkg", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	deepLink := filepath.Join(base, "deep-link")
	if err := os.Symlink(deep, deepLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	want := canonicalTestPath(t, realRoot)
	for _, input := range []string{realRoot, rootLink, deep, deepLink} {
		got, err := ResolveModuleRoot(input)
		if err != nil {
			t.Fatalf("ResolveModuleRoot(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ResolveModuleRoot(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveModuleRootNestedModule(t *testing.T) {
	parent := t.TempDir()
	writeRootGoMod(t, parent)
	sub := filepath.Join(parent, "sub")
	writeRootGoMod(t, sub)
	deep := filepath.Join(sub, "internal", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(parent, "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		parent:  canonicalTestPath(t, parent),
		sibling: canonicalTestPath(t, parent),
		sub:     canonicalTestPath(t, sub),
		deep:    canonicalTestPath(t, sub),
	}
	for input, want := range cases {
		got, err := ResolveModuleRoot(input)
		if err != nil {
			t.Fatalf("ResolveModuleRoot(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ResolveModuleRoot(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveModuleRootIgnoresGoModDirectory(t *testing.T) {
	parent := t.TempDir()
	writeRootGoMod(t, parent)
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(filepath.Join(child, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModuleRoot(child)
	if err != nil {
		t.Fatal(err)
	}
	if want := canonicalTestPath(t, parent); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestResolveModuleRootMissingModule(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveModuleRoot(dir); err == nil {
		t.Fatal("expected an error outside a Go module")
	} else if !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("error = %v", err)
	}
	if _, err := ResolveModuleRoot(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}

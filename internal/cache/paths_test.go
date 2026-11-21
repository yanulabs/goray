package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateCacheHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GORAY_DB", "")
}

func TestSplitPathsShareSummaryAcrossCheckouts(t *testing.T) {
	isolateCacheHome(t)
	first := filepath.Join(t.TempDir(), "checkout-a")
	second := filepath.Join(t.TempDir(), "checkout-b")
	writeCacheModule(t, first, map[string]string{})
	writeCacheModule(t, second, map[string]string{})

	firstWork, err := DefaultPath(first)
	if err != nil {
		t.Fatal(err)
	}
	secondWork, err := DefaultPath(second)
	if err != nil {
		t.Fatal(err)
	}
	firstSummary, err := DefaultSummaryPath(first)
	if err != nil {
		t.Fatal(err)
	}
	secondSummary, err := DefaultSummaryPath(second)
	if err != nil {
		t.Fatal(err)
	}

	if firstWork == secondWork {
		t.Fatalf("distinct checkouts share working DB: %q", firstWork)
	}
	if firstSummary != secondSummary {
		t.Fatalf("same module has different summary DBs: %q != %q", firstSummary, secondSummary)
	}
	if filepath.Base(filepath.Dir(firstWork)) != "worktrees" {
		t.Fatalf("working DB directory = %q", filepath.Dir(firstWork))
	}
	if filepath.Base(filepath.Dir(firstSummary)) != "summaries" {
		t.Fatalf("summary DB directory = %q", filepath.Dir(firstSummary))
	}
	if !strings.HasPrefix(filepath.Base(firstWork), "checkout-a-") {
		t.Fatalf("working DB name = %q", filepath.Base(firstWork))
	}
	if !strings.HasPrefix(filepath.Base(firstSummary), "cachetest-") {
		t.Fatalf("summary DB name = %q", filepath.Base(firstSummary))
	}
	for _, path := range []string{firstWork, secondWork, firstSummary} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("path derivation created %q: %v", path, err)
		}
	}
}

func TestResolvePathsSingleDBOverride(t *testing.T) {
	isolateCacheHome(t)
	project := t.TempDir()
	writeCacheModule(t, project, map[string]string{})

	work, summary, err := ResolvePaths(project, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if work != ":memory:" || summary != ":memory:" {
		t.Fatalf("memory paths = %q, %q", work, summary)
	}

	work, summary, err = ResolvePaths(project, "custom.db")
	if err != nil {
		t.Fatal(err)
	}
	if work != summary || !filepath.IsAbs(work) || filepath.Base(work) != "custom.db" {
		t.Fatalf("override paths = %q, %q", work, summary)
	}

	t.Setenv("GORAY_DB", filepath.Join(t.TempDir(), "env.db"))
	work, summary, err = ResolvePaths(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if work != summary || filepath.Base(work) != "env.db" {
		t.Fatalf("env paths = %q, %q", work, summary)
	}
}

func TestReadModulePath(t *testing.T) {
	cases := map[string]string{
		"module example.com/plain\n":               "example.com/plain",
		"module \"example.com/quoted\"\n":          "example.com/quoted",
		"module example.com/comment // trailing\n": "example.com/comment",
		"// head\nmodule `example.com/raw`\n":      "example.com/raw",
	}
	for body, want := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ReadModulePath(dir)
		if err != nil {
			t.Fatalf("ReadModulePath(%q): %v", body, err)
		}
		if got != want {
			t.Fatalf("ReadModulePath(%q) = %q, want %q", body, got, want)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("go 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadModulePath(dir); err == nil {
		t.Fatal("go.mod without a module directive should fail")
	}
}

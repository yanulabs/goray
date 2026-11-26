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

func TestWorkingDBPathVariesByBuildTags(t *testing.T) {
	isolateCacheHome(t)
	project := t.TempDir()
	writeCacheModule(t, project, map[string]string{})

	plain, err := ResolvePathWithTags(project, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := ResolvePathWithTags(project, "", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	ba, err := ResolvePathWithTags(project, "", []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	integration, err := ResolvePathWithTags(project, "", []string{"integration"})
	if err != nil {
		t.Fatal(err)
	}
	if plain == ab || ab == integration {
		t.Fatalf("tag sets share a working DB: plain=%q ab=%q integration=%q", plain, ab, integration)
	}
	if ab != ba {
		t.Fatalf("tag order changed the working DB: %q != %q", ab, ba)
	}

	plainSummary, err := DefaultSummaryPathWithTags(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	taggedSummary, err := DefaultSummaryPathWithTags(project, []string{"integration"})
	if err != nil {
		t.Fatal(err)
	}
	if plainSummary != taggedSummary {
		t.Fatalf("build tags changed the shared summary DB: %q != %q", plainSummary, taggedSummary)
	}
}

func TestResolvePathsWorkspace(t *testing.T) {
	isolateCacheHome(t)
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	writeCacheModule(t, first, map[string]string{})
	writeCacheModule(t, second, map[string]string{})
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.23\n\nuse (\n\t./first\n\t./second\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	work, summary, err := ResolvePaths(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if work == "" || summary == "" {
		t.Fatalf("workspace paths = %q, %q", work, summary)
	}
	firstSummary, err := DefaultSummaryPath(first)
	if err != nil {
		t.Fatal(err)
	}
	if summary == firstSummary {
		t.Fatalf("workspace summary DB collides with a module summary: %q", summary)
	}
	if filepath.Base(filepath.Dir(summary)) != "summaries" {
		t.Fatalf("workspace summary directory = %q", filepath.Dir(summary))
	}
}

func TestWorkspaceAndModuleCachePathsAreDistinct(t *testing.T) {
	isolateCacheHome(t)
	t.Setenv("GOWORK", "")
	root := t.TempDir()
	writeCacheModule(t, root, map[string]string{})
	child := filepath.Join(root, "child")
	writeNamedModule(t, child, "example.com/cachetest/child")
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.23\n\nuse (\n\t.\n\t./child\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	singleWork, singleSummary, err := ResolvePathsWithOptions(root, "", PathOptions{})
	if err != nil {
		t.Fatal(err)
	}
	workspaceWork, workspaceSummary, err := ResolvePathsWithOptions(root, "", PathOptions{Workspace: true})
	if err != nil {
		t.Fatal(err)
	}
	if singleWork == workspaceWork {
		t.Fatalf("single-module and workspace indexes share working DB %q", singleWork)
	}
	if singleSummary == workspaceSummary {
		t.Fatalf("single-module and workspace indexes share summary DB %q", singleSummary)
	}
	if identity, err := projectIdentity(root, false); err != nil || identity != "example.com/cachetest" {
		t.Fatalf("module identity = %q (%v)", identity, err)
	}
	if identity, err := projectIdentity(root, true); err != nil || identity != "workspace:example.com/cachetest,example.com/cachetest/child" {
		t.Fatalf("workspace identity = %q (%v)", identity, err)
	}
}

func TestCustomGOWORKFile(t *testing.T) {
	isolateCacheHome(t)
	t.Setenv("GOWORK", "")
	base := t.TempDir()
	alpha := filepath.Join(base, "alpha")
	beta := filepath.Join(base, "beta")
	writeNamedModule(t, alpha, "example.com/alpha")
	writeNamedModule(t, beta, "example.com/beta")

	workDir := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(workDir, "custom.work")
	body := "go 1.23\n\nuse (\n\t" + filepath.ToSlash(alpha) + "\n\t" + filepath.ToSlash(beta) + "\n)\n"
	if err := os.WriteFile(custom, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", custom)

	identity, err := projectIdentity(workDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "workspace:example.com/alpha,example.com/beta" {
		t.Fatalf("identity = %q", identity)
	}
	work, summary, err := ResolvePathsWithOptions(workDir, "", PathOptions{Workspace: true})
	if err != nil {
		t.Fatal(err)
	}
	if work == "" || summary == "" {
		t.Fatalf("workspace paths = %q, %q", work, summary)
	}
}

func writeNamedModule(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "module " + module + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
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

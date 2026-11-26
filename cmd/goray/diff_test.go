package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/diff"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeDiffFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupDiffRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeDiffFile(t, root, "go.mod", "module example.com/diff\n\ngo 1.23\n")
	writeDiffFile(t, root, "cmd/app/main.go", `package main

import (
	"example.com/diff/api"
	"example.com/diff/modules/alpha/one"
)

func main() {
	_ = one.Use()
	_ = api.Stable(1)
}
`)
	writeDiffFile(t, root, "api/api.go", `package api

type Removed struct{}

func Stable(x int) int { return x }
`)
	writeDiffFile(t, root, "modules/alpha/one/one.go", `package one

import "example.com/diff/modules/beta/one"

func Use() int { return one.Value() }
`)
	writeDiffFile(t, root, "modules/alpha/two/two.go", `package two

func Value() int { return 2 }
`)
	writeDiffFile(t, root, "modules/beta/one/one.go", `package one

func Value() int { return 1 }
`)
	writeDiffFile(t, root, "internal/dead/dead.go", `package dead

func UsedByNothing() {}
`)

	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")
	return root
}

func modifyDiffRepo(t *testing.T, root string) {
	t.Helper()
	writeDiffFile(t, root, "modules/beta/two/two.go", `package two

import "example.com/diff/modules/alpha/two"

func Value() int { return two.Value() }
`)
	writeDiffFile(t, root, "api/api.go", `package api

func Stable(x int64) int64 { return x }
`)
	writeDiffFile(t, root, "internal/dead/dead.go", `package dead

func UsedByNothing() {}

func newDeadHelper() {}
`)
	writeDiffFile(t, root, "modules/alpha/one/hotspot.go", `package one

func Complex(x int) int {
	total := 0
	if x > 1 {
		total++
	}
	if x > 2 {
		total++
	}
	if x > 3 {
		total++
	}
	if x > 4 {
		total++
	}
	if x > 5 {
		total++
	}
	if x > 6 {
		total++
	}
	if x > 7 {
		total++
	}
	if x > 8 {
		total++
	}
	if x > 9 {
		total++
	}
	if x > 10 && total > 2 {
		total++
	}
	return total
}
`)
}

func TestDiffJSONAndGates(t *testing.T) {
	root := setupDiffRepo(t)
	modifyDiffRepo(t, root)

	var stdout, stderr bytes.Buffer
	if err := runDiff([]string{"HEAD", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDiff: %v\nstderr=%s", err, stderr.String())
	}
	var report diff.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if report.BaseRef != "HEAD" {
		t.Fatalf("base ref = %q", report.BaseRef)
	}
	head := gitRun(t, root, "rev-parse", "HEAD")
	if report.Head != head || report.BaseCommit != head {
		t.Fatalf("head/base commit = %q/%q, want %q", report.Head, report.BaseCommit, head)
	}
	if len(report.Cycles.New) == 0 {
		t.Fatalf("new cycles missing: %+v", report.Cycles)
	}
	cycle := strings.Join(report.Cycles.New, " ")
	if !strings.Contains(cycle, "alpha") || !strings.Contains(cycle, "beta") {
		t.Fatalf("cycle = %s", cycle)
	}
	if len(report.API.Removed) == 0 || report.API.Removed[0].Name != "Removed" {
		t.Fatalf("removed API = %+v", report.API.Removed)
	}
	if len(report.API.Modified) == 0 || report.API.Modified[0].Name != "Stable" {
		t.Fatalf("modified API = %+v", report.API.Modified)
	}
	if report.API.Modified[0].Before == "" || report.API.Modified[0].After == "" || report.API.Modified[0].Before == report.API.Modified[0].After {
		t.Fatalf("modified API signatures = %+v", report.API.Modified[0])
	}
	if len(report.Hotspots.New) == 0 || report.Hotspots.New[0].Name != "Complex" {
		t.Fatalf("new hotspots = %+v", report.Hotspots.New)
	}
	if len(report.Dead.New) == 0 || report.Dead.New[0].Name != "newDeadHelper" {
		t.Fatalf("new dead code = %+v", report.Dead.New)
	}
	if report.Summary.Symbols <= report.Summary.SymbolsBase {
		t.Fatalf("symbol counters = %+v", report.Summary)
	}

	list := gitRun(t, root, "worktree", "list", "--porcelain")
	if strings.Count(list, "worktree ") != 1 || strings.Contains(list, "goray-diff-") {
		t.Fatalf("leaked worktrees:\n%s", list)
	}

	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "cycles"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 1 {
		t.Fatalf("cycles gate exit = %d", code)
	}
	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "breaking_api,untested_hotspots,dead_code"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 1 {
		t.Fatalf("combined gate exit = %d", code)
	}
	if code := runCLI([]string{"diff", "HEAD", root, "--format", "json"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("diff without gates exit = %d", code)
	}
}

func TestDiffInvalidBaseRefAndNonGit(t *testing.T) {
	root := setupDiffRepo(t)
	if code := runCLI([]string{"diff", "does-not-exist", root}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 2 {
		t.Fatalf("invalid base ref exit = %d", code)
	}
	if code := runCLI([]string{"diff", "HEAD", root}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("clean HEAD exit = %d", code)
	}
	t.Chdir(t.TempDir())
	if code := runCLI([]string{"diff"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 2 {
		t.Fatalf("non-git exit = %d", code)
	}
}

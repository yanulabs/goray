package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/diff"
	"github.com/yanulabs/goray/internal/index"
)

func writeArchFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitArchRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitArchRepo(t *testing.T, root string) {
	t.Helper()
	gitArchRun(t, root, "init", "-q")
	gitArchRun(t, root, "config", "user.email", "test@example.com")
	gitArchRun(t, root, "config", "user.name", "Test")
	gitArchRun(t, root, "add", ".")
	gitArchRun(t, root, "commit", "-q", "-m", "base")
}

func setupArchRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeArchFile(t, root, "go.mod", "module example.com/arch\n\ngo 1.23\n")
	writeArchFile(t, root, "cmd/app/main.go", `package main

import (
	"example.com/arch/api"
	"example.com/arch/modules/alpha/one"
)

func main() {
	_ = one.Use()
	_ = api.Stable(1)
}
`)
	writeArchFile(t, root, "api/api.go", `package api

type Removed struct{}

func Stable(x int) int { return x }
`)
	writeArchFile(t, root, "modules/alpha/one/one.go", `package one

import "example.com/arch/modules/beta/one"

func Use() int { return one.Value() }
`)
	writeArchFile(t, root, "modules/alpha/two/two.go", "package two\n\nfunc Value() int { return 2 }\n")
	writeArchFile(t, root, "modules/beta/one/one.go", "package one\n\nfunc Value() int { return 1 }\n")
	writeArchFile(t, root, "internal/dead/dead.go", "package dead\n\nfunc UsedByNothing() {}\n")
	commitArchRepo(t, root)
	return root
}

func modifyArchRepo(t *testing.T, root string) {
	t.Helper()
	writeArchFile(t, root, "modules/beta/two/two.go", `package two

import "example.com/arch/modules/alpha/two"

func Value() int { return two.Value() }
`)
	writeArchFile(t, root, "api/api.go", `package api

func Stable(x int64) int64 { return x }
`)
	writeArchFile(t, root, "internal/dead/dead.go", `package dead

func UsedByNothing() {}

func newDeadHelper() {}
`)
	writeArchFile(t, root, "modules/alpha/one/hotspot.go", `package one

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

func buildArchIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	idx, err := index.BuildWithOptionsContext(context.Background(), root, index.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func decodeArchReport(t *testing.T, text string) diff.Report {
	t.Helper()
	var report diff.Report
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, text)
	}
	return report
}

func assertNoLeakedWorktrees(t *testing.T, root string) {
	t.Helper()
	list := gitArchRun(t, root, "worktree", "list", "--porcelain")
	if strings.Count(list, "worktree ") != 1 || strings.Contains(list, "goray-diff-") {
		t.Fatalf("leaked worktrees:\n%s", list)
	}
}

func TestGetArchitectureDiffMatchesCLIJSON(t *testing.T) {
	root := setupArchRepo(t)
	modifyArchRepo(t, root)
	live := buildArchIndex(t, root)
	gates := []string{diff.GateCycles, diff.GateDeadCode, diff.GateBreakingAPI, diff.GateUntestedHotspots}

	cliReport, err := diff.BuildContext(context.Background(), diff.Options{
		BaseRef: "HEAD",
		Path:    root,
		FailOn:  gates,
	})
	if err != nil {
		t.Fatalf("CLI report: %v", err)
	}
	tool := toolMap(t, live)["get_architecture_diff"]
	text := mustRunTool(t, tool, map[string]any{
		"base_ref": "HEAD",
		"fail_on":  []any{"cycles", "dead_code", "breaking_api", "untested_hotspots"},
	})
	toolReport := decodeArchReport(t, text)

	cliJSON, err := json.Marshal(cliReport)
	if err != nil {
		t.Fatal(err)
	}
	toolJSON, err := json.Marshal(toolReport)
	if err != nil {
		t.Fatal(err)
	}
	var cliView, toolView map[string]any
	if err := json.Unmarshal(cliJSON, &cliView); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(toolJSON, &toolView); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cliView, toolView) {
		t.Fatalf("tool JSON differs from CLI JSON\nCLI:  %s\nTool: %s", cliJSON, toolJSON)
	}

	if len(toolReport.Cycles.New) == 0 || len(toolReport.API.Removed) == 0 || len(toolReport.API.Modified) == 0 || len(toolReport.Dead.New) == 0 || len(toolReport.Hotspots.New) == 0 {
		t.Fatalf("fixture did not exercise every diff section: %+v", toolReport)
	}
	if !reflect.DeepEqual(toolReport.Failed, gates) {
		// Gate failures are successful query results; mustRunTool already
		// asserted failed=false.
		t.Fatalf("failed gates = %v, want %v", toolReport.Failed, gates)
	}
	assertNoLeakedWorktrees(t, root)
}

func TestGetArchitectureDiffReusesLiveSnapshot(t *testing.T) {
	root := setupArchRepo(t)
	live := buildArchIndex(t, root)
	// Breaking the working tree after the snapshot was built would fail a
	// working-tree re-index. The tool must serve the in-memory head snapshot.
	writeArchFile(t, root, "api/api.go", "package api\n\nfunc Broken() int { return \"x\" }\n")

	tool := toolMap(t, live)["get_architecture_diff"]
	text := mustRunTool(t, tool, map[string]any{"base_ref": "HEAD"})
	report := decodeArchReport(t, text)
	if len(report.API.Added)+len(report.API.Modified)+len(report.API.Removed) != 0 {
		t.Fatalf("stale working tree should compare clean against HEAD: %+v", report.API)
	}
	if report.Summary.Symbols != report.Summary.SymbolsBase {
		t.Fatalf("summary reused a different head snapshot: %+v", report.Summary)
	}
	assertNoLeakedWorktrees(t, root)
}

func TestGetArchitectureDiffAdditiveChangePassesBreakingGate(t *testing.T) {
	root := setupArchRepo(t)
	writeArchFile(t, root, "api/api.go", `package api

type Removed struct{}

func Stable(x int) int { return x }

func Added() {}
`)
	live := buildArchIndex(t, root)
	tool := toolMap(t, live)["get_architecture_diff"]
	text := mustRunTool(t, tool, map[string]any{"base_ref": "HEAD", "fail_on": "breaking_api"})
	report := decodeArchReport(t, text)
	if len(report.API.Added) == 0 {
		t.Fatalf("added API missing: %+v", report.API)
	}
	if len(report.API.Removed) != 0 || len(report.API.Modified) != 0 {
		t.Fatalf("additive change marked breaking: %+v", report.API)
	}
	if len(report.Failed) != 0 {
		t.Fatalf("additive change failed breaking_api gate: %v", report.Failed)
	}
	assertNoLeakedWorktrees(t, root)
}

func TestGetArchitectureDiffCleansWorktreeWhenBaseBuildFails(t *testing.T) {
	root := t.TempDir()
	writeArchFile(t, root, "go.mod", "module example.com/brokenbase\n\ngo 1.23\n")
	writeArchFile(t, root, "a/a.go", "package a\n\nfunc Broken() int { return \"x\" }\n")
	commitArchRepo(t, root)

	live := &index.Index{
		RootDir:  root,
		Packages: map[string]*index.PkgInfo{},
		Symbols:  map[string]*index.Symbol{},
	}
	tool := toolMap(t, live)["get_architecture_diff"]
	text, failed := tool.Run(context.Background(), map[string]any{"base_ref": "HEAD"})
	if !failed {
		t.Fatalf("broken base ref unexpectedly succeeded: %s", text)
	}
	if !strings.Contains(text, "index base ref") {
		t.Fatalf("unexpected error: %s", text)
	}
	assertNoLeakedWorktrees(t, root)
}

func TestGetArchitectureDiffRejectsUnknownGate(t *testing.T) {
	root := setupArchRepo(t)
	tool := toolMap(t, buildArchIndex(t, root))["get_architecture_diff"]
	text, failed := tool.Run(context.Background(), map[string]any{"fail_on": "cycles,nope"})
	if !failed || !strings.Contains(text, "unknown") {
		t.Fatalf("gate validation = %q, failed=%v", text, failed)
	}
}

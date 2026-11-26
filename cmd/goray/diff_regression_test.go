package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/diff"
)

// apiDiffRepo creates a repository with externally importable API packages and
// closed-world internal/cmd packages.
func apiDiffRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeDiffFile(t, root, "go.mod", "module example.com/apidiff\n\ngo 1.23\n")
	writeDiffFile(t, root, "api/api.go", `package api

type Config struct {
	Name string
}

type hiddenConfig struct {
	Value string
}

type Store interface {
	Get(id int) error
}

func Stable(x int) int { return x }
`)
	writeDiffFile(t, root, "internal/impl/impl.go", `package impl

func Helper(x int) int { return x }
`)
	writeDiffFile(t, root, "cmd/tool/main.go", `package main

func main() {}

func ExportedCommand(x int) int { return x }
`)
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")
	return root
}

func findAPIChange(items []diff.APIChange, id string) (diff.APIChange, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return diff.APIChange{}, false
}

func TestDiffBreakingAPITracksFieldsAndInterfaceSignatures(t *testing.T) {
	root := apiDiffRepo(t)
	writeDiffFile(t, root, "api/api.go", `package api

type Config struct {
	Name  int
	Extra bool
}

type hiddenConfig struct {
	Value int
}

type Store interface {
	Get(id int64) (int, error)
}

func Stable(x int) int { return x }
`)
	writeDiffFile(t, root, "internal/impl/impl.go", `package impl

func Helper(x int64) int64 { return x }
`)
	writeDiffFile(t, root, "cmd/tool/main.go", `package main

func main() {}

func ExportedCommand(x int64) int64 { return x }
`)

	var stdout, stderr bytes.Buffer
	if err := runDiff([]string{"HEAD", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDiff: %v\nstderr=%s", err, stderr.String())
	}
	var report diff.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}

	fieldID := "example.com/apidiff/api.Config.Name"
	field, ok := findAPIChange(report.API.Modified, fieldID)
	if !ok {
		t.Fatalf("field type change not detected; modified=%+v", report.API.Modified)
	}
	if field.Kind != "field" || field.Name != "Config.Name" || field.Before != "string" || field.After != "int" {
		t.Fatalf("field change = %+v", field)
	}
	if _, ok := findAPIChange(report.API.Added, "example.com/apidiff/api.Config.Extra"); !ok {
		t.Fatalf("new exported field not reported; added=%+v", report.API.Added)
	}

	ifaceID := "sym:example.com/apidiff/api.Store"
	iface, ok := findAPIChange(report.API.Modified, ifaceID)
	if !ok {
		t.Fatalf("interface signature change not detected; modified=%+v", report.API.Modified)
	}
	if iface.Kind != "interface" || !strings.Contains(iface.Before, "int") || !strings.Contains(iface.After, "int64") {
		t.Fatalf("interface change = %+v", iface)
	}

	for _, item := range report.API.Modified {
		if strings.Contains(item.ID, "/internal/") || strings.Contains(item.ID, "/cmd/") {
			t.Fatalf("closed-world package leaked into breaking API: %+v", item)
		}
		if strings.Contains(item.ID, "hiddenConfig") {
			t.Fatalf("field of unexported type leaked into breaking API: %+v", item)
		}
	}

	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "breaking_api"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 1 {
		t.Fatalf("breaking_api gate exit = %d, want 1", code)
	}
}

func TestDiffBreakingAPIGateIgnoresFieldAdditionsAndClosedWorld(t *testing.T) {
	root := apiDiffRepo(t)
	writeDiffFile(t, root, "api/api.go", `package api

type Config struct {
	Name  string
	Extra bool
}

type Store interface {
	Get(id int) error
}

func Stable(x int) int { return x }
`)
	writeDiffFile(t, root, "internal/impl/impl.go", `package impl

func Helper(x int64) int64 { return x }
`)
	writeDiffFile(t, root, "cmd/tool/main.go", `package main

func main() {}

func ExportedCommand(x int64) int64 { return x }
`)

	var stdout, stderr bytes.Buffer
	if err := runDiff([]string{"HEAD", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDiff: %v\nstderr=%s", err, stderr.String())
	}
	var report diff.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(report.API.Added) == 0 {
		t.Fatalf("expected added field, got %+v", report.API)
	}
	if len(report.API.Modified) != 0 || len(report.API.Removed) != 0 {
		t.Fatalf("additive/closed-world changes marked breaking: modified=%+v removed=%+v", report.API.Modified, report.API.Removed)
	}
	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "breaking_api"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("breaking_api gate exit = %d, want 0", code)
	}
}

func complexFunction(name string, conditions int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "func %s(x int) int {\n\ttotal := 0\n", name)
	for i := 1; i <= conditions; i++ {
		fmt.Fprintf(&b, "\tif x > %d {\n\t\ttotal++\n\t}\n", i)
	}
	b.WriteString("\treturn total\n}\n")
	return b.String()
}

func hotspotRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeDiffFile(t, root, "go.mod", "module example.com/hotdiff\n\ngo 1.23\n")
	writeDiffFile(t, root, "hot/hot.go", "package hot\n\n"+complexFunction("Complex", 12)+"\n"+complexFunction("Covered", 12))
	writeDiffFile(t, root, "hot/hot_test.go", `package hot

import "testing"

func TestCovered(t *testing.T) {
	if Covered(1) == 0 {
		t.Fatal("unexpected")
	}
}
`)
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")
	return root
}

func TestDiffHotspotGateOnlyFailsOnRegressionsOrLostCoverage(t *testing.T) {
	root := hotspotRepo(t)
	// Complex improves but remains an untested hotspot; Covered loses its test
	// with unchanged complexity.
	writeDiffFile(t, root, "hot/hot.go", "package hot\n\n"+complexFunction("Complex", 11)+"\n"+complexFunction("Covered", 12))
	if err := os.Remove(filepath.Join(root, "hot", "hot_test.go")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runDiff([]string{"HEAD", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDiff: %v\nstderr=%s", err, stderr.String())
	}
	var report diff.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Hotspots.New) != 0 {
		t.Fatalf("unexpected new hotspots: %+v", report.Hotspots.New)
	}
	if len(report.Hotspots.TopDelta) != 1 {
		t.Fatalf("top deltas = %+v, want only the coverage loss", report.Hotspots.TopDelta)
	}
	lost := report.Hotspots.TopDelta[0]
	if lost.Name != "Covered" || !lost.CoverageLost {
		t.Fatalf("lost coverage delta = %+v", lost)
	}
	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "untested_hotspots"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 1 {
		t.Fatalf("untested_hotspots gate exit = %d, want 1", code)
	}
}

func TestDiffHotspotGatePassesImprovements(t *testing.T) {
	root := hotspotRepo(t)
	writeDiffFile(t, root, "hot/hot.go", "package hot\n\n"+complexFunction("Complex", 11)+"\n"+complexFunction("Covered", 12))
	// Keep the test for the unchanged, still-covered function.
	var stdout, stderr bytes.Buffer
	if err := runDiff([]string{"HEAD", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDiff: %v\nstderr=%s", err, stderr.String())
	}
	var report diff.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Hotspots.New) != 0 || len(report.Hotspots.TopDelta) != 0 {
		t.Fatalf("improvement reported as regression: new=%+v delta=%+v", report.Hotspots.New, report.Hotspots.TopDelta)
	}
	if code := runCLI([]string{"diff", "HEAD", root, "--fail-on", "untested_hotspots"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("untested_hotspots gate exit = %d, want 0", code)
	}
}

func TestParseDiffArgsSinglePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cases := []struct {
		args []string
		base string
		path string
	}{
		{args: []string{"HEAD", "./sub"}, base: "HEAD", path: "./sub"},
		{args: []string{"./sub"}, base: "HEAD", path: "./sub"},
		{args: []string{"../parent"}, base: "HEAD", path: "../parent"},
		{args: []string{"sub"}, base: "HEAD", path: "sub"},
		{args: []string{"."}, base: "HEAD", path: "."},
		{args: []string{"main"}, base: "main", path: "."},
		{args: []string{"v1.2.3"}, base: "v1.2.3", path: "."},
	}
	for _, tc := range cases {
		opts, err := parseDiffArgs(tc.args)
		if err != nil {
			t.Fatalf("parseDiffArgs(%v): %v", tc.args, err)
		}
		if opts.baseRef != tc.base || opts.path != tc.path {
			t.Fatalf("parseDiffArgs(%v) = base %q path %q, want %q/%q", tc.args, opts.baseRef, opts.path, tc.base, tc.path)
		}
	}
}

func TestDiffSubdirectoryAndSymlinkedWorktree(t *testing.T) {
	root := setupDiffRepo(t)
	modifyDiffRepo(t, root)
	subdir := filepath.Join(root, "modules", "alpha")

	if err := runDiff([]string{"HEAD", subdir, "--format", "json"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("diff subdirectory: %v", err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := runDiff([]string{"HEAD", link, "--format", "json"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("diff symlinked worktree: %v", err)
	}
}

func TestBuildDiffContextCancellation(t *testing.T) {
	root := setupDiffRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildDiffContext(ctx, diffOptions{baseRef: "HEAD", path: root, format: "json"}, nil); err == nil {
		t.Fatal("expected canceled diff to fail")
	}
}

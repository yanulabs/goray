package index

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTagModule(t *testing.T, dir, module string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gomod := "module " + module + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"integration, e2e", "integration", "", "linux"})
	want := []string{"e2e", "integration", "linux"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

func TestBuildTagsAffectTestReachability(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/tags", map[string]string{
		"main.go": `package main

func defaultOnly() {}
func integrationOnly() {}

func main() { defaultOnly() }
`,
		"main_test.go": `package main

import "testing"

func TestDefault(t *testing.T) { defaultOnly() }
`,
		"integration_test.go": `//go:build integration

package main

import "testing"

func TestIntegration(t *testing.T) { integrationOnly() }
`,
	})

	plain, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	defaultID := "example.com/tags.defaultOnly"
	integrationID := "example.com/tags.integrationOnly"
	if !plain.TestCovered[defaultID] {
		t.Fatalf("default test coverage missing: %v", plain.TestCovered)
	}
	if plain.TestCovered[integrationID] {
		t.Fatalf("integration-only function covered without tags: %v", plain.TestCovered)
	}

	tagged, err := BuildWithOptions(root, BuildOptions{Tags: []string{"integration"}})
	if err != nil {
		t.Fatal(err)
	}
	if !tagged.TestCovered[defaultID] || !tagged.TestCovered[integrationID] {
		t.Fatalf("tagged coverage = %v", tagged.TestCovered)
	}
	if len(tagged.BuildTags) != 1 || tagged.BuildTags[0] != "integration" {
		t.Fatalf("build tags = %v", tagged.BuildTags)
	}
}

func TestWorkspaceBuild(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, filepath.Join(root, "modulea"), "example.com/a", map[string]string{
		"a.go": `package a

import "example.com/b"

func UseB() int { return b.Value() }
`,
	})
	writeTagModule(t, filepath.Join(root, "moduleb"), "example.com/b", map[string]string{
		"b.go": `package b

func Value() int { return 1 }
`,
	})
	work := `go 1.23

use (
	./modulea
	./moduleb
)
`
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := BuildWithOptions(root, BuildOptions{Workspace: true})
	if err != nil {
		t.Fatal(err)
	}
	if !idx.Workspace {
		t.Fatal("workspace flag not recorded")
	}
	if len(idx.ModulePaths) != 2 {
		t.Fatalf("module paths = %v", idx.ModulePaths)
	}
	for _, pkg := range []string{"example.com/a", "example.com/b"} {
		if idx.Packages[pkg] == nil {
			t.Fatalf("missing workspace package %s: %v", pkg, len(idx.Packages))
		}
	}
	valueID := "example.com/b.Value"
	if len(idx.XRefs[valueID]) == 0 {
		t.Fatalf("cross-module reference missing: %v", idx.XRefs)
	}
	if got := idx.XRefs[valueID][0].FromPkg; got != "example.com/a" {
		t.Fatalf("cross-module ref from %q", got)
	}
	for _, area := range []string{"a", "b"} {
		if idx.Modules[area] == nil {
			t.Fatalf("missing module area %q: %v", area, idx.SortedModuleNames())
		}
	}
}

func TestTestOnlyPackageAndModuleClassification(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/testonly", map[string]string{
		"prod/prod.go": "package prod\n\nfunc Live() {}\n",
		"gates/gate_test.go": `package gates

import "testing"

func TestGate(t *testing.T) {}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	gates := idx.Packages["example.com/testonly/gates"]
	if gates == nil {
		t.Fatalf("test-only package missing: %v", len(idx.Packages))
	}
	if !gates.TestOnly || gates.ProdLOC != 0 || gates.TestLOC == 0 {
		t.Fatalf("gates = %+v", gates)
	}
	if mod := idx.Modules["gates"]; mod == nil || !mod.TestOnly || mod.ProdLOC != 0 || mod.TestLOC == 0 {
		t.Fatalf("gates module = %+v", mod)
	}
	prod := idx.Packages["example.com/testonly/prod"]
	if prod == nil || prod.TestOnly {
		t.Fatalf("prod = %+v", prod)
	}
	if mod := idx.Modules["prod"]; mod == nil || mod.TestOnly {
		t.Fatalf("prod module = %+v", mod)
	}
}

func TestGoWorkOffDisablesWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, filepath.Join(root, "modulea"), "example.com/a", map[string]string{
		"a.go": "package a\n",
	})
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.23\n\nuse ./modulea\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	if _, ok := FindGoWork(root); ok {
		t.Fatal("GOWORK=off must disable detection")
	}
	if _, err := ResolveProjectRoot(root, true); err == nil {
		t.Fatal("expected an error resolving a workspace with GOWORK=off")
	}
}

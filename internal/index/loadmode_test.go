package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModeDoesNotParseDependencySyntax(t *testing.T) {
	root := t.TempDir()
	writeFile := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/loadmode\n\ngo 1.26\n")
	writeFile("sample.go", `package loadmode

import "fmt"

type Value struct{}

func (Value) String() string { return "" }

var _ fmt.Stringer = Value{}
`)

	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.pkgs) != 1 {
		t.Fatalf("packages = %d", len(idx.pkgs))
	}
	for _, pkg := range idx.pkgs {
		for importPath, imported := range pkg.Imports {
			if len(imported.Syntax) != 0 {
				t.Fatalf("dependency %s was parsed: %d syntax files", importPath, len(imported.Syntax))
			}
		}
	}
	if !idx.IfaceMethods["example.com/loadmode.Value.String"] {
		t.Fatal("imported fmt.Stringer was not matched")
	}
}

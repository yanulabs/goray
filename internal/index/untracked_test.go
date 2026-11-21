package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDetectUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")

	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":          "module example.com/nested\n\ngo 1.23\n",
		"tracked.go":      "package nested\n",
		"untracked.go":    "package nested\n",
		"with space.go":   "package nested\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(nested, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, root, "add", "nested/go.mod", "nested/tracked.go")

	got := detectUntracked(nested)
	for _, want := range []string{"untracked.go", "with space.go"} {
		if !got[want] {
			t.Fatalf("%q should be untracked; got %v", want, got)
		}
	}
	for _, notWant := range []string{"tracked.go", "go.mod"} {
		if got[notWant] {
			t.Fatalf("%q should not be untracked; got %v", notWant, got)
		}
	}

	outside := t.TempDir()
	if len(detectUntracked(outside)) != 0 {
		t.Fatalf("outside git repo should yield no untracked files: %v", detectUntracked(outside))
	}
}

func TestBuildTagsUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")

	mod := filepath.Join(root, "app")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/app\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "tracked.go"), []byte("package app\n\nfunc Tracked() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "scratch.go"), []byte("package app\n\nfunc Scratch() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "app/go.mod", "app/tracked.go")

	idx, err := Build(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !idx.UntrackedFiles["scratch.go"] || idx.UntrackedFiles["tracked.go"] {
		t.Fatalf("untracked set = %v", idx.UntrackedFiles)
	}
	if sym := idx.Symbols["example.com/app.Scratch"]; sym == nil || !sym.Untracked {
		t.Fatalf("Scratch should be tagged untracked: %+v", sym)
	}
	if sym := idx.Symbols["example.com/app.Tracked"]; sym == nil || sym.Untracked {
		t.Fatalf("Tracked should be tracked: %+v", sym)
	}
	modSummary := idx.Modules["root"]
	if modSummary == nil || modSummary.UntrackedFiles != 1 || modSummary.AllUntracked {
		t.Fatalf("module summary = %+v", modSummary)
	}
	view := idx.TrackedView()
	if len(view.Symbols) != len(idx.Symbols)-1 || view.Symbols["example.com/app.Scratch"] != nil {
		t.Fatalf("tracked view did not drop untracked symbol: %d vs %d", len(view.Symbols), len(idx.Symbols))
	}
}

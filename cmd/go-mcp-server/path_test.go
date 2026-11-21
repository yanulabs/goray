package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestCLI(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "go-mcp-server")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

func cliEnv(cacheHome string, overrides map[string]string) []string {
	values := map[string]string{
		"HOME":           cacheHome,
		"XDG_CACHE_HOME": filepath.Join(cacheHome, "xdg"),
		"GO_MCP_DB":      "",
	}
	for key, value := range overrides {
		values[key] = value
	}
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func runTestCLI(t *testing.T, binary, dir string, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeTestModule(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "module " + module + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dbFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".db") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestDBPathCLI(t *testing.T) {
	binary := buildTestCLI(t)
	cacheHome := t.TempDir()
	env := cliEnv(cacheHome, nil)

	base := t.TempDir()
	root := filepath.Join(base, "My Repo")
	writeTestModule(t, root, "example.com/app")
	deep := filepath.Join(root, "internal", "cache")
	file := filepath.Join(deep, "summary.go")
	writeTestFile(t, file, "package cache\n")

	stdout, stderr, err := runTestCLI(t, binary, root, env, "-db-path")
	if err != nil {
		t.Fatalf("db-path: %v (%s)", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("db-path stderr = %q", stderr)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("db-path stdout = %q", stdout)
	}
	path := strings.TrimSuffix(stdout, "\n")
	if !filepath.IsAbs(path) {
		t.Fatalf("db path is not absolute: %q", path)
	}
	if !strings.HasPrefix(filepath.Base(path), "my-repo-") {
		t.Fatalf("db name = %q", filepath.Base(path))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("db-path created the DB file: %v", err)
	}
	if files := dbFilesUnder(t, root); len(files) > 0 {
		t.Fatalf("dotfiles/db files inside module: %v", files)
	}

	for _, input := range []string{root, deep, file} {
		out, errText, err := runTestCLI(t, binary, base, env, "-db-path", input)
		if err != nil {
			t.Fatalf("db-path %q: %v (%s)", input, err, errText)
		}
		if out != stdout {
			t.Fatalf("db-path %q = %q, want %q", input, out, stdout)
		}
	}

	t.Run("memory override", func(t *testing.T) {
		out, errText, err := runTestCLI(t, binary, deep, env, "-db-path", "-db", ":memory:")
		if err != nil {
			t.Fatalf("db-path: %v (%s)", err, errText)
		}
		if out != ":memory:\n" {
			t.Fatalf("memory path = %q", out)
		}
	})

	t.Run("env override", func(t *testing.T) {
		env := cliEnv(cacheHome, map[string]string{"GO_MCP_DB": ":memory:"})
		out, errText, err := runTestCLI(t, binary, deep, env, "-db-path")
		if err != nil {
			t.Fatalf("db-path: %v (%s)", err, errText)
		}
		if out != ":memory:\n" {
			t.Fatalf("env memory path = %q", out)
		}
	})

	t.Run("file override", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom.db")
		out, errText, err := runTestCLI(t, binary, deep, env, "-db-path", "-db", custom)
		if err != nil {
			t.Fatalf("db-path: %v (%s)", err, errText)
		}
		if out != custom+"\n" {
			t.Fatalf("custom path = %q, want %q", out, custom+"\n")
		}
		if _, err := os.Stat(custom); !os.IsNotExist(err) {
			t.Fatalf("db-path created custom DB: %v", err)
		}
	})
}

func TestDBPathCLISymlinksAndNestedModules(t *testing.T) {
	binary := buildTestCLI(t)
	cacheHome := t.TempDir()
	env := cliEnv(cacheHome, nil)
	base := t.TempDir()

	root := filepath.Join(base, "parent")
	writeTestModule(t, root, "example.com/parent")
	deep := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	rootPath, _, err := runTestCLI(t, binary, root, env, "-db-path")
	if err != nil {
		t.Fatal(err)
	}

	rootLink := filepath.Join(base, "parent-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	deepLink := filepath.Join(base, "deep-link")
	if err := os.Symlink(deep, deepLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, input := range []string{rootLink, deepLink} {
		out, errText, err := runTestCLI(t, binary, base, env, "-db-path", input)
		if err != nil {
			t.Fatalf("db-path %q: %v (%s)", input, err, errText)
		}
		if out != rootPath {
			t.Fatalf("db-path %q = %q, want %q", input, out, rootPath)
		}
	}

	sub := filepath.Join(root, "sub")
	writeTestModule(t, sub, "example.com/parent/sub")
	subDeep := filepath.Join(sub, "internal", "deep")
	writeTestFile(t, filepath.Join(subDeep, "x.go"), "package deep\n")

	subPath, _, err := runTestCLI(t, binary, subDeep, env, "-db-path")
	if err != nil {
		t.Fatal(err)
	}
	if subPath == rootPath {
		t.Fatalf("nested module reused parent db path: %q", subPath)
	}
	if !strings.HasPrefix(filepath.Base(strings.TrimSuffix(subPath, "\n")), "sub-") {
		t.Fatalf("nested module db name = %q", filepath.Base(strings.TrimSuffix(subPath, "\n")))
	}
	parentAgain, _, err := runTestCLI(t, binary, deep, env, "-db-path")
	if err != nil {
		t.Fatal(err)
	}
	if parentAgain != rootPath {
		t.Fatalf("parent module path = %q, want %q", parentAgain, rootPath)
	}
}

func TestDBPathCLIOutsideModuleCreatesNoDB(t *testing.T) {
	binary := buildTestCLI(t)
	dir := t.TempDir()
	cacheHome := t.TempDir()
	env := cliEnv(cacheHome, nil)

	for _, args := range [][]string{
		{"-db-path"},
		{"-db-info"},
		{"-db-path", "-db", ":memory:"},
		{"-db-info", "-db", ":memory:"},
	} {
		stdout, stderr, err := runTestCLI(t, binary, dir, env, args...)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
		if stdout != "" {
			t.Fatalf("%v stdout = %q", args, stdout)
		}
		if !strings.Contains(stderr, "go.mod") {
			t.Fatalf("%v stderr = %q", args, stderr)
		}
	}
	if files := dbFilesUnder(t, dir); len(files) > 0 {
		t.Fatalf("db files created in non-module: %v", files)
	}
	if files := dbFilesUnder(t, cacheHome); len(files) > 0 {
		t.Fatalf("db files created in cache dir: %v", files)
	}
}

func TestDBPathCLIDoesNotLoadPackages(t *testing.T) {
	binary := buildTestCLI(t)
	cacheHome := t.TempDir()
	env := cliEnv(cacheHome, nil)
	root := filepath.Join(t.TempDir(), "broken")
	writeTestModule(t, root, "example.com/broken")
	writeTestFile(t, filepath.Join(root, "broken.go"), "package broken\n\nfunc Broken( {\n")

	stdout, stderr, err := runTestCLI(t, binary, root, env, "-db-path")
	if err != nil {
		t.Fatalf("db-path should not load packages: %v (%s)", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("db-path produced no path")
	}
}

func TestDBInfoUsesModuleRoot(t *testing.T) {
	binary := buildTestCLI(t)
	cacheHome := t.TempDir()
	env := cliEnv(cacheHome, nil)
	root := filepath.Join(t.TempDir(), "repo")
	writeTestModule(t, root, "example.com/repo")
	deep := filepath.Join(root, "internal", "deep")
	writeTestFile(t, filepath.Join(deep, "x.go"), "package deep\n")

	fromRoot, stderr, err := runTestCLI(t, binary, root, env, "-db-info")
	if err != nil {
		t.Fatalf("db-info root: %v (%s)", err, stderr)
	}
	fromDeep, stderr, err := runTestCLI(t, binary, deep, env, "-db-info")
	if err != nil {
		t.Fatalf("db-info deep: %v (%s)", err, stderr)
	}
	var rootStats, deepStats struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(fromRoot), &rootStats); err != nil {
		t.Fatalf("root db-info JSON: %v\n%s", err, fromRoot)
	}
	if err := json.Unmarshal([]byte(fromDeep), &deepStats); err != nil {
		t.Fatalf("deep db-info JSON: %v\n%s", err, fromDeep)
	}
	if rootStats.Path == "" || rootStats.Path != deepStats.Path {
		t.Fatalf("db-info paths differ: %q vs %q", rootStats.Path, deepStats.Path)
	}
}

func TestExportFromSubdirectoryResolvesModuleRoot(t *testing.T) {
	binary := buildTestCLI(t)
	root := filepath.Join(t.TempDir(), "repo")
	writeTestModule(t, root, "example.com/check")
	writeTestFile(t, filepath.Join(root, "rootpkg", "root.go"), "package rootpkg\n\nfunc Root() int { return 1 }\n")
	deep := filepath.Join(root, "deep", "pkg")
	writeTestFile(t, filepath.Join(deep, "deep.go"), "package pkg\n\nfunc Deep() int { return 2 }\n")

	output := filepath.Join(t.TempDir(), "map.md")
	stdout, stderr, err := runTestCLI(t, binary, deep, cliEnv(t.TempDir(), nil), "-export-md", output, "-log-level", "off")
	if err != nil {
		t.Fatalf("export-md: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"example.com/check", "`rootpkg`", "`deep`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export missing %q:\n%s", want, text)
		}
	}
}

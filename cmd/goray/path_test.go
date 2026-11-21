package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	binary := filepath.Join(t.TempDir(), "goray")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
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

func cliEnv(cacheHome string, overrides map[string]string) []string {
	values := map[string]string{
		"HOME":           cacheHome,
		"XDG_CACHE_HOME": filepath.Join(cacheHome, "xdg"),
		"GORAY_DB":       "",
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

func isolateCLIEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GORAY_DB", "")
}

func runDirect(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runDirectOpener(t, func(string) error { return nil }, args...)
}

func runDirectOpener(t *testing.T, opener func(string) error, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(args, &stdout, &stderr, opener)
	return code, stdout.String(), stderr.String()
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

func newTestModule(t *testing.T) (root, deep, file string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "My Repo")
	writeTestModule(t, root, "example.com/check")
	writeTestFile(t, filepath.Join(root, "rootpkg", "root.go"), "package rootpkg\n\nfunc Root() int { return 1 }\n")
	deep = filepath.Join(root, "internal", "cache")
	file = filepath.Join(deep, "summary.go")
	writeTestFile(t, file, "package cache\n\nfunc Leaf() int { return 2 }\n")
	return root, deep, file
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

func TestHelpCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		code, stdout, stderr := runDirect(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		for _, want := range []string{"Usage:", "serve [path]", "export <html|md|dot>", "db <info|path|rm|prune>", "index [path]", "GORAY_DB", "GORAY_LOG_LEVEL", "goray db path"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v help missing %q:\n%s", args, want, stdout)
			}
		}
	}

	cases := map[string][]string{
		"serve":  {"serve", "--help"},
		"export": {"export", "--help"},
		"db":     {"db", "--help"},
		"html":   {"export", "html", "--help"},
	}
	for name, args := range cases {
		code, stdout, stderr := runDirect(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%s help: code=%d stderr=%q", name, code, stderr)
		}
		if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "Examples:") {
			t.Fatalf("%s help is incomplete:\n%s", name, stdout)
		}
	}

	code, stdout, _ := runDirect(t, "help", "serve")
	if code != 0 || !strings.Contains(stdout, "--no-watch") || !strings.Contains(stdout, "--port") || !strings.Contains(stdout, "--open") {
		t.Fatalf("help serve = code %d\n%s", code, stdout)
	}
}

func TestVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		code, stdout, stderr := runDirect(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		if !strings.HasPrefix(stdout, "goray ") {
			t.Fatalf("%v output = %q", args, stdout)
		}
	}
	code, stdout, stderr := runDirect(t, "-version")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown flag") || !strings.Contains(stderr, "goray help") {
		t.Fatalf("old -version: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runDirect(t, "version", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "takes no arguments") {
		t.Fatalf("version extra: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUnknownAndInvalidCommands(t *testing.T) {
	cases := [][]string{
		{"bogus"},
		{"serve", "--bogus"},
		{"export", "nope"},
		{"export"},
		{"db"},
		{"db", "nope"},
		{"help", "bogus"},
		{"serve", ".", "extra"},
		{"db", "path", ".", "extra"},
	}
	for _, args := range cases {
		code, stdout, stderr := runDirect(t, args...)
		if code != 2 {
			t.Fatalf("%v: code=%d, want 2", args, code)
		}
		if stdout != "" {
			t.Fatalf("%v stdout = %q", args, stdout)
		}
		if !strings.Contains(stderr, "goray:") || !strings.Contains(stderr, "Run 'goray help <command>' for usage.") {
			t.Fatalf("%v stderr = %q", args, stderr)
		}
	}
}

func TestFlagOrderingAndParsing(t *testing.T) {
	exportCases := []struct {
		args []string
		path string
		out  string
		db   string
	}{
		{[]string{"html", ".", "-o", "report.html"}, ".", "report.html", ""},
		{[]string{"html", "-o", "report.html", "."}, ".", "report.html", ""},
		{[]string{"html", "--out=report.html", "."}, ".", "report.html", ""},
		{[]string{"html", ".", "--out", "report.html"}, ".", "report.html", ""},
		{[]string{"html", "--db=:memory:", "."}, ".", "", ":memory:"},
		{[]string{"html", "--", "-repo"}, "-repo", "", ""},
	}
	for _, tc := range exportCases {
		opts, err := parseExportArgs(tc.args)
		if err != nil {
			t.Fatalf("parseExportArgs(%v): %v", tc.args, err)
		}
		if opts.path != tc.path || opts.out != tc.out || opts.dbPath != tc.db {
			t.Fatalf("parseExportArgs(%v) = %+v", tc.args, opts)
		}
	}
	if _, err := parseExportArgs([]string{"html", ".", "extra"}); err == nil {
		t.Fatal("extra export path should fail")
	}
	if _, err := parseExportArgs([]string{"dot", "--open"}); err == nil {
		t.Fatal("--open should fail for dot")
	}
	if _, err := parseExportArgs([]string{"md", "--open"}); err == nil {
		t.Fatal("--open should fail for md")
	}
	if _, err := parseExportArgs([]string{"dot", "-o"}); err == nil {
		t.Fatal("missing -o value should fail")
	}
	if opts, err := parseExportArgs([]string{"dot", "-o", "-h"}); err != nil || opts.out != "-h" {
		t.Fatalf("flag-looking output value was not preserved: %+v (%v)", opts, err)
	}
	if wantsHelp([]string{"dot", "-o", "-h"}, exportValueFlags) {
		t.Fatal("-h used as an -o value must not trigger help")
	}
	if !wantsHelp([]string{"dot", "--help"}, exportValueFlags) {
		t.Fatal("--help after a format should trigger help")
	}
	if wantsHelp([]string{"--", "--help"}, nil) {
		t.Fatal("--help after -- must be positional")
	}

	serveCases := []struct {
		args    []string
		path    string
		db      string
		log     string
		port    int
		noWatch bool
		open    bool
	}{
		{[]string{"--db", ":memory:", "/repo", "--no-watch", "--log-level", "error", "--port", "0", "--open"}, "/repo", ":memory:", "error", 0, true, true},
		{[]string{"/repo", "--no-watch"}, "/repo", "", envOr("GORAY_LOG_LEVEL", "info"), defaultServePort, true, false},
		{[]string{"--port", "5050", "/repo"}, "/repo", "", envOr("GORAY_LOG_LEVEL", "info"), 5050, false, false},
		{[]string{"--", "--no-watch"}, "--no-watch", "", envOr("GORAY_LOG_LEVEL", "info"), defaultServePort, false, false},
	}
	for _, tc := range serveCases {
		opts, err := parseServeArgs(tc.args)
		if err != nil {
			t.Fatalf("parseServeArgs(%v): %v", tc.args, err)
		}
		if opts.path != tc.path || opts.dbPath != tc.db || opts.logLevel != tc.log || opts.port != tc.port || opts.noWatch != tc.noWatch || opts.open != tc.open {
			t.Fatalf("parseServeArgs(%v) = %+v", tc.args, opts)
		}
	}
	if _, err := parseServeArgs([]string{".", "extra"}); err == nil {
		t.Fatal("extra serve path should fail")
	}
	if _, err := parseServeArgs([]string{"--port", "70000"}); err == nil {
		t.Fatal("out-of-range port should fail")
	}

	dbCases := []struct {
		args []string
		cmd  string
		path string
		db   string
	}{
		{[]string{"path", "/repo", "--db", ":memory:"}, "path", "/repo", ":memory:"},
		{[]string{"path", "--db", ":memory:", "/repo"}, "path", "/repo", ":memory:"},
		{[]string{"info", "/repo"}, "info", "/repo", ""},
	}
	for _, tc := range dbCases {
		opts, err := parseDBArgs(tc.args)
		if err != nil {
			t.Fatalf("parseDBArgs(%v): %v", tc.args, err)
		}
		if opts.command != tc.cmd || opts.path != tc.path || opts.dbPath != tc.db {
			t.Fatalf("parseDBArgs(%v) = %+v", tc.args, opts)
		}
	}
}

func TestExportOutputs(t *testing.T) {
	isolateCLIEnv(t)
	root, deep, _ := newTestModule(t)

	dotFile := filepath.Join(t.TempDir(), "deps.dot")
	code, stdout, stderr := runDirect(t, "export", "dot", root, "-o", dotFile)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("export dot -o: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	body, err := os.ReadFile(dotFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "digraph") {
		t.Fatalf("dot output = %q", string(body))
	}

	code, stdout, stderr = runDirect(t, "export", "dot", root)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "digraph") {
		t.Fatalf("export dot stdout: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}

	mdFile := filepath.Join(t.TempDir(), "ARCHITECTURE.md")
	code, stdout, stderr = runDirect(t, "export", "md", deep, "--out", mdFile, "-db", ":memory:")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("export md -o: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	body, err = os.ReadFile(mdFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Architecture Map:", "`rootpkg`", "`internal/cache`", "## Summary Cache"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("markdown missing %q:\n%s", want, body)
		}
	}

	code, stdout, stderr = runDirect(t, "export", "md", root, "-db", ":memory:")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "# Architecture Map:") {
		t.Fatalf("export md stdout: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}

	htmlFile := filepath.Join(t.TempDir(), "report.html")
	code, stdout, stderr = runDirect(t, "export", "html", "--out", htmlFile, root, "-db", ":memory:")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("export html -o: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	body, err = os.ReadFile(htmlFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `<script id="report-data"`) || !strings.Contains(string(body), "goray · Architecture Report") {
		t.Fatal("html report is missing expected content")
	}

	code, stdout, stderr = runDirect(t, "export", "html", root, "-db", ":memory:")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `<script id="report-data"`) {
		t.Fatalf("export html stdout: code=%d stderr=%q", code, stderr)
	}

	outA := filepath.Join(t.TempDir(), "a.dot")
	outB := filepath.Join(t.TempDir(), "b.dot")
	if code, _, stderr := runDirect(t, "export", "dot", root, "-o", outA); code != 0 {
		t.Fatalf("-o failed: %s", stderr)
	}
	if code, _, stderr := runDirect(t, "export", "dot", root, "--out", outB); code != 0 {
		t.Fatalf("--out failed: %s", stderr)
	}
	a, _ := os.ReadFile(outA)
	b, _ := os.ReadFile(outB)
	if !bytes.Equal(a, b) {
		t.Fatal("-o and --out produced different output")
	}
	if files := dbFilesUnder(t, root); len(files) > 0 {
		t.Fatalf("export created DB files inside module: %v", files)
	}
}

func TestExportOpen(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)

	var opened []string
	opener := func(path string) error {
		opened = append(opened, path)
		return nil
	}
	code, stdout, stderr := runDirectOpener(t, opener, "export", "html", "--open", root, "-db", ":memory:")
	if code != 0 || stdout != "" {
		t.Fatalf("export html --open: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(opened) != 1 {
		t.Fatalf("opened = %v", opened)
	}
	tempPath := opened[0]
	if strings.TrimSpace(stderr) != tempPath {
		t.Fatalf("stderr = %q, opened = %q", stderr, tempPath)
	}
	if !strings.HasPrefix(filepath.Base(tempPath), "goray-") || !strings.HasSuffix(tempPath, "-report.html") {
		t.Fatalf("temp report path = %q", tempPath)
	}
	t.Cleanup(func() { _ = os.Remove(tempPath) })
	if body, err := os.ReadFile(tempPath); err != nil || !strings.Contains(string(body), "<title>goray") {
		t.Fatalf("temp report = %v", err)
	}

	custom := filepath.Join(t.TempDir(), "custom.html")
	code, stdout, stderr = runDirectOpener(t, opener, "export", "html", "-o", custom, "--open", root, "-db", ":memory:")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("export html -o --open: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(opened) != 2 || opened[1] != custom {
		t.Fatalf("opened = %v", opened)
	}

	for _, format := range []string{"md", "dot"} {
		code, stdout, stderr = runDirect(t, "export", format, "--open", root)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "--open") {
			t.Fatalf("export %s --open: code=%d stdout=%q stderr=%q", format, code, stdout, stderr)
		}
	}

	code, _, stderr = runDirectOpener(t, func(string) error { return errors.New("no browser") },
		"export", "html", "--open", root, "-db", ":memory:")
	if code != 1 || !strings.Contains(stderr, "no browser") {
		t.Fatalf("opener failure: code=%d stderr=%q", code, stderr)
	}
}

func TestDBCommands(t *testing.T) {
	isolateCLIEnv(t)
	root, deep, file := newTestModule(t)

	code, stdout, stderr := runDirect(t, "db", "path", root)
	if code != 0 || stderr != "" {
		t.Fatalf("db path: code=%d stderr=%q", code, stderr)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("db path stdout = %q", stdout)
	}
	dbPath := strings.TrimSuffix(stdout, "\n")
	if !filepath.IsAbs(dbPath) || !strings.HasPrefix(filepath.Base(dbPath), "my-repo-") {
		t.Fatalf("db path = %q", dbPath)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("db path created the DB: %v", err)
	}
	for _, input := range []string{deep, file} {
		outCode, out, errText := runDirect(t, "db", "path", input)
		if outCode != 0 || strings.TrimSuffix(out, "\n") != dbPath {
			t.Fatalf("db path %q = %q (code %d, stderr %q)", input, out, outCode, errText)
		}
	}

	code, stdout, stderr = runDirect(t, "db", "path", deep, "--db", ":memory:")
	if code != 0 || stdout != ":memory:\n" || stderr != "" {
		t.Fatalf("db path memory: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	t.Setenv("GORAY_DB", ":memory:")
	code, stdout, stderr = runDirect(t, "db", "path", deep)
	if code != 0 || stdout != ":memory:\n" || stderr != "" {
		t.Fatalf("db path env: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	t.Setenv("GORAY_DB", "")

	code, stdout, stderr = runDirect(t, "db", "info", root, "-db", ":memory:")
	if code != 0 || stderr != "" {
		t.Fatalf("db info: code=%d stderr=%q", code, stderr)
	}
	var stats struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &stats); err != nil {
		t.Fatalf("db info JSON: %v\n%s", err, stdout)
	}
	if stats.Path != ":memory:" {
		t.Fatalf("db info path = %q", stats.Path)
	}
}

func TestDBPathOutsideModuleCreatesNoDB(t *testing.T) {
	isolateCLIEnv(t)
	dir := t.TempDir()
	cacheHome := os.Getenv("HOME")
	for _, args := range [][]string{
		{"db", "path", dir},
		{"db", "info", dir},
		{"db", "path", dir, "-db", ":memory:"},
	} {
		code, stdout, stderr := runDirect(t, args...)
		if code != 1 {
			t.Fatalf("%v: code=%d, want 1", args, code)
		}
		if stdout != "" || !strings.Contains(stderr, "go.mod") {
			t.Fatalf("%v: stdout=%q stderr=%q", args, stdout, stderr)
		}
	}
	if files := dbFilesUnder(t, dir); len(files) > 0 {
		t.Fatalf("db files created in non-module: %v", files)
	}
	if files := dbFilesUnder(t, cacheHome); len(files) > 0 {
		t.Fatalf("db files created in cache dir: %v", files)
	}
}

func TestDBPathDoesNotLoadPackages(t *testing.T) {
	isolateCLIEnv(t)
	root := filepath.Join(t.TempDir(), "broken")
	writeTestModule(t, root, "example.com/broken")
	writeTestFile(t, filepath.Join(root, "broken.go"), "package broken\n\nfunc Broken( {\n")
	code, stdout, stderr := runDirect(t, "db", "path", root)
	if code != 0 || stderr != "" || strings.TrimSpace(stdout) == "" {
		t.Fatalf("db path broken module: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDBPathSymlinksAndNestedModules(t *testing.T) {
	isolateCLIEnv(t)
	base := t.TempDir()
	root := filepath.Join(base, "parent")
	writeTestModule(t, root, "example.com/parent")
	deep := filepath.Join(root, "internal", "deep")
	writeTestFile(t, filepath.Join(deep, "x.go"), "package deep\n")

	rootCode, rootOut, rootErr := runDirect(t, "db", "path", root)
	if rootCode != 0 {
		t.Fatal(rootErr)
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
		outCode, out, errText := runDirect(t, "db", "path", input)
		if outCode != 0 || out != rootOut {
			t.Fatalf("db path %q = %q (code %d, stderr %q)", input, out, outCode, errText)
		}
	}

	sub := filepath.Join(root, "sub")
	writeTestModule(t, sub, "example.com/parent/sub")
	subDeep := filepath.Join(sub, "internal", "deep")
	writeTestFile(t, filepath.Join(subDeep, "y.go"), "package deep\n")
	subCode, subOut, subErr := runDirect(t, "db", "path", subDeep)
	if subCode != 0 {
		t.Fatal(subErr)
	}
	if subOut == rootOut {
		t.Fatalf("nested module reused parent path: %q", subOut)
	}
}

func TestDBPathSummariesAndSplitLayout(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)
	other := filepath.Join(t.TempDir(), "Other Repo")
	writeTestModule(t, other, "example.com/check")

	workCode, workOut, workErr := runDirect(t, "db", "path", root)
	if workCode != 0 || workErr != "" {
		t.Fatalf("db path: code=%d stderr=%q", workCode, workErr)
	}
	summaryCode, summaryOut, summaryErr := runDirect(t, "db", "path", root, "--summaries")
	if summaryCode != 0 || summaryErr != "" {
		t.Fatalf("db path --summaries: code=%d stderr=%q", summaryCode, summaryErr)
	}
	for name, out := range map[string]string{"work": workOut, "summary": summaryOut} {
		if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
			t.Fatalf("%s path stdout = %q", name, out)
		}
	}
	workPath := strings.TrimSuffix(workOut, "\n")
	summaryPath := strings.TrimSuffix(summaryOut, "\n")
	if filepath.Base(filepath.Dir(workPath)) != "worktrees" {
		t.Fatalf("working DB directory = %q", filepath.Dir(workPath))
	}
	if filepath.Base(filepath.Dir(summaryPath)) != "summaries" {
		t.Fatalf("summary DB directory = %q", filepath.Dir(summaryPath))
	}
	for _, path := range []string{workPath, summaryPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("db path created %q: %v", path, err)
		}
	}

	_, otherWork, _ := runDirect(t, "db", "path", other)
	_, otherSummary, _ := runDirect(t, "db", "path", other, "--summaries")
	if strings.TrimSuffix(otherWork, "\n") == workPath {
		t.Fatal("distinct checkouts share a working DB")
	}
	if strings.TrimSuffix(otherSummary, "\n") != summaryPath {
		t.Fatalf("same module summary path = %q, want %q", otherSummary, summaryPath)
	}

	code, stdout, stderr := runDirect(t, "db", "path", root, "--summaries", "--db", ":memory:")
	if code != 0 || stdout != ":memory:\n" || stderr != "" {
		t.Fatalf("summary override: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runDirect(t, "db", "info", root)
	if code != 0 || stderr != "" {
		t.Fatalf("db info: code=%d stderr=%q", code, stderr)
	}
	var stats struct {
		Path          string `json:"path"`
		DBPath        string `json:"db_path"`
		SummaryDBPath string `json:"summary_db_path"`
	}
	if err := json.Unmarshal([]byte(stdout), &stats); err != nil {
		t.Fatalf("db info JSON: %v\n%s", err, stdout)
	}
	if stats.Path != workPath || stats.DBPath != workPath || stats.SummaryDBPath != summaryPath {
		t.Fatalf("db info paths = %+v", stats)
	}
	for _, path := range []string{workPath, summaryPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("db info did not create %q: %v", path, err)
		}
	}
}

func TestExplicitDBModeAvoidsSummaryDirectory(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)

	singlePath := filepath.Join(t.TempDir(), "single.db")
	cases := []struct {
		name string
		args []string
		path string
	}{
		{"file", []string{"index", "--json", root, "--db", singlePath}, singlePath},
		{"memory", []string{"index", "--json", root, "--db", ":memory:"}, ":memory:"},
	}
	for _, tc := range cases {
		code, stdout, stderr := runDirect(t, tc.args...)
		if code != 0 {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", tc.name, code, stdout, stderr)
		}
		var result indexResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("%s JSON: %v\n%s", tc.name, err, stdout)
		}
		if result.DBPath != tc.path || result.SummaryDBPath != tc.path {
			t.Fatalf("%s paths = %q, %q", tc.name, result.DBPath, result.SummaryDBPath)
		}
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	gorayDir := filepath.Join(cacheRoot, "goray")
	if _, statErr := os.Stat(gorayDir); statErr == nil {
		if files := dbFilesUnder(t, gorayDir); len(files) > 0 {
			t.Fatalf("explicit DB mode touched the cache directory: %v", files)
		}
	} else if !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
	if _, err := os.Stat(filepath.Join(gorayDir, "summaries")); !os.IsNotExist(err) {
		t.Fatalf("explicit DB mode created summaries directory: %v", err)
	}
}

func TestGORAYLogLevelEnv(t *testing.T) {
	isolateCLIEnv(t)
	root, _, _ := newTestModule(t)
	t.Setenv("GORAY_LOG_LEVEL", "verbose")
	code, stdout, stderr := runDirect(t, "serve", root, "-db", ":memory:")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "invalid log level") || !strings.Contains(stderr, "goray help") {
		t.Fatalf("invalid log level: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestExportFromSubdirectoryResolvesModuleRoot(t *testing.T) {
	isolateCLIEnv(t)
	root, deep, _ := newTestModule(t)
	code, stdout, stderr := runDirect(t, "export", "md", deep, "-db", ":memory:")
	if code != 0 || stderr != "" {
		t.Fatalf("export md deep: code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{"example.com/check", "`rootpkg`", "`internal/cache`"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("export missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, root) {
		// The report uses relative package/module labels; this is just a sanity
		// check that the deep path was resolved rather than treated as a module.
		t.Logf("report labels do not include the absolute root %q", root)
	}
}

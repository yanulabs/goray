package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportHTMLCLI(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	project := filepath.Join(repoRoot, "internal", "index", "testdata", "standard")
	binary := buildTestCLI(t)
	env := cliEnv(t.TempDir(), map[string]string{"GORAY_DB": ":memory:"})

	output := filepath.Join(t.TempDir(), "report.html")
	stdout, stderr, err := runTestCLI(t, binary, cwd, env, "export", "html", project, "-o", output)
	if err != nil {
		t.Fatalf("export html: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("export html stdout = %q", stdout)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if len(html) < 10_000 {
		t.Fatalf("report too small: %d bytes", len(html))
	}
	if !strings.Contains(html, `<script id="report-data" type="application/json">`) {
		t.Fatal("report data payload is missing")
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Fatal("report contains an external URL")
	}
	if strings.Contains(html, "</script><img") {
		t.Fatal("report contains an unescaped script breakout")
	}
}

func TestCLIProcessExitCodes(t *testing.T) {
	binary := buildTestCLI(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	env := cliEnv(t.TempDir(), nil)

	stdout, stderr, err := runTestCLI(t, binary, cwd, env, "version")
	if err != nil || !strings.HasPrefix(stdout, "goray ") || stderr != "" {
		t.Fatalf("version: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	stdout, stderr, err = runTestCLI(t, binary, cwd, env, "--help")
	if err != nil || !strings.Contains(stdout, "Usage:") || stderr != "" {
		t.Fatalf("--help: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	stdout, stderr, err = runTestCLI(t, binary, cwd, env, "bogus")
	if err == nil {
		t.Fatal("unknown command should fail")
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("unknown command error = %v", err)
	}
	if stdout != "" || !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Fatalf("unknown command stdout=%q stderr=%q", stdout, stderr)
	}
}

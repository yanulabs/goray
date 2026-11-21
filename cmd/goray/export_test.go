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
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "goray")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = cwd
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	output := filepath.Join(tmp, "report.html")
	cmd := exec.Command(binary,
		"-export-html", output,
		"-db", ":memory:",
		"-log-level", "off",
		project,
	)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("export-html: %v\n%s", err, out)
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

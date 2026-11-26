package index

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseErrorPosition(t *testing.T) {
	cases := []struct {
		pos  string
		file string
		line int
		col  int
	}{
		{pos: "", file: ""},
		{pos: "-"},
		{pos: "main.go", file: "main.go"},
		{pos: "pkg/main.go:12", file: "pkg/main.go", line: 12},
		{pos: "pkg/main.go:12:4", file: "pkg/main.go", line: 12, col: 4},
		{pos: "/tmp/project/main.go:3:9", file: "/tmp/project/main.go", line: 3, col: 9},
		{pos: `C:\project\main.go:7:2`, file: `C:\project\main.go`, line: 7, col: 2},
		{pos: "weird:name", file: "weird:name"},
		{pos: "weird:name:9", file: "weird:name", line: 9},
		{pos: "leading/trailing.go:1:2", file: "leading/trailing.go", line: 1, col: 2},
	}
	for _, test := range cases {
		file, line, col := parseErrorPosition(test.pos)
		if file != test.file || line != test.line || col != test.col {
			t.Errorf("parseErrorPosition(%q) = (%q, %d, %d), want (%q, %d, %d)",
				test.pos, file, line, col, test.file, test.line, test.col)
		}
	}
}

func TestDiagnosticFileNormalizesRootRelativePaths(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "pkg", "a.go")
	if got := diagnosticFile(root, inside); got != "pkg/a.go" {
		t.Fatalf("inside path = %q", got)
	}
	if got := diagnosticFile(root, "pkg/a.go"); got != "pkg/a.go" {
		t.Fatalf("relative path = %q", got)
	}
	outside := filepath.Join(t.TempDir(), "other.go")
	if got := diagnosticFile(root, outside); got != filepath.ToSlash(outside) {
		t.Fatalf("outside path = %q", got)
	}
	if got := diagnosticFile(root, "-"); got != "" {
		t.Fatalf("dash path = %q", got)
	}
}

func TestBuildCapturesParseDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeTagModule(t, dir, "example.com/parse", map[string]string{
		"a/a.go": "package a\n\nfunc Broken() int { if\n",
	})
	_, err := BuildWithOptionsContext(context.Background(), dir, BuildOptions{})
	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("error = %v, want *LoadError", err)
	}
	parse := diagnosticByKind(loadErr.Diagnostics, DiagnosticKindParse)
	if parse == nil {
		t.Fatalf("parse diagnostic missing: %+v", loadErr.Diagnostics)
	}
	if parse.Package != "example.com/parse/a" || parse.File != "a/a.go" || parse.Line != 3 || parse.Col == 0 {
		t.Fatalf("parse diagnostic = %+v", parse)
	}
	if parse.IsTest {
		t.Fatalf("production parse diagnostic flagged as test: %+v", parse)
	}
	if !strings.Contains(parse.Message, "expected") && !strings.Contains(parse.Message, "syntax error") {
		t.Fatalf("parse message = %q", parse.Message)
	}
}

func TestDiagnosticsFromErrorFallsBackToListDiagnostic(t *testing.T) {
	got := diagnosticsFromError(errors.New("no packages found"))
	if len(got) != 1 || got[0].Kind != DiagnosticKindList || got[0].Message != "no packages found" {
		t.Fatalf("diagnostics = %+v", got)
	}
	if got := diagnosticsFromError(nil); got != nil {
		t.Fatalf("nil error diagnostics = %+v", got)
	}
}

func TestStoreDiagnosticsKeepHealthySnapshotOnProductionError(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a/a.go": "package a\n\nfunc Value() int { return 1 }\n",
	}
	writeTagModule(t, dir, "example.com/diag", files)
	store := NewStore(dir, nil)
	store.Start()
	ctx := context.Background()
	before, err := store.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Symbols["example.com/diag/a.Value"] == nil {
		t.Fatal("initial symbol missing")
	}

	files["a/a.go"] = "package a\n\nfunc Value() int { return \"x\" }\n"
	writeTagModule(t, dir, "example.com/diag", files)
	if err := store.Rebuild(); err == nil {
		t.Fatal("production type error rebuild should fail")
	}
	if store.State() != StateError {
		t.Fatalf("state = %s", store.State())
	}
	after, err := store.Wait(ctx)
	if err != nil {
		t.Fatalf("last healthy snapshot lost: %v", err)
	}
	if after != before {
		t.Fatal("failed rebuild replaced the healthy snapshot")
	}
	if after.Symbols["example.com/diag/a.Value"] == nil {
		t.Fatal("healthy snapshot symbol missing after failed rebuild")
	}
	status := store.Status()
	if status.LoadErrors == 0 || len(status.LoadErrorDetails) == 0 {
		t.Fatalf("legacy load errors missing: %+v", status)
	}
	if !strings.Contains(strings.Join(status.LoadErrorDetails, "\n"), "example.com/diag/a: ") {
		t.Fatalf("legacy load error details = %v", status.LoadErrorDetails)
	}

	diagnostics := store.Diagnostics()
	production := diagnosticByKind(diagnostics, DiagnosticKindType)
	if production == nil {
		t.Fatalf("structured type diagnostic missing: %+v", diagnostics)
	}
	if production.Package != "example.com/diag/a" || production.File != "a/a.go" || production.Line != 3 || production.Col == 0 {
		t.Fatalf("production diagnostic = %+v", production)
	}
	if production.IsTest {
		t.Fatalf("production diagnostic flagged as test: %+v", production)
	}
	if !strings.Contains(production.Message, "cannot use") {
		t.Fatalf("production message = %q", production.Message)
	}

	files["a/a.go"] = "package a\n\nfunc Value() int { return 2 }\n"
	writeTagModule(t, dir, "example.com/diag", files)
	if err := store.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if store.State() != StateReady {
		t.Fatalf("recovered state = %s", store.State())
	}
	if got := store.Diagnostics(); len(got) != 0 {
		t.Fatalf("diagnostics after recovery = %+v", got)
	}
	recovered, err := store.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Symbols["example.com/diag/a.Value"] == nil {
		t.Fatal("recovered symbol missing")
	}
}

func TestStoreDiagnosticsCaptureTestOnlyErrors(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a/a.go":      "package a\n\nfunc Value() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { missingName() }\n",
	}
	writeTagModule(t, dir, "example.com/diag", files)
	store := NewStore(dir, nil)
	store.Start()
	idx, err := store.Wait(context.Background())
	if err != nil {
		t.Fatalf("test-only error aborted production build: %v", err)
	}
	if store.State() != StateReady {
		t.Fatalf("state = %s", store.State())
	}
	status := store.Status()
	if status.LoadErrors != 0 {
		t.Fatalf("production load errors = %+v", status)
	}
	if status.TestLoadErrors == 0 || len(status.TestLoadErrorDetails) == 0 {
		t.Fatalf("legacy test load errors missing: %+v", status)
	}
	if len(idx.Diagnostics) == 0 {
		t.Fatal("index diagnostics missing")
	}
	diagnostics := store.Diagnostics()
	if len(diagnostics) != len(idx.Diagnostics) {
		t.Fatalf("store diagnostics = %+v, index diagnostics = %+v", diagnostics, idx.Diagnostics)
	}
	testDiagnostic := diagnosticByKind(diagnostics, DiagnosticKindType)
	if testDiagnostic == nil {
		t.Fatalf("structured test diagnostic missing: %+v", diagnostics)
	}
	if !testDiagnostic.IsTest || testDiagnostic.Package != "example.com/diag/a" || testDiagnostic.File != "a/a_test.go" {
		t.Fatalf("test diagnostic = %+v", testDiagnostic)
	}
	if testDiagnostic.Line != 5 || testDiagnostic.Col == 0 {
		t.Fatalf("test diagnostic position = %+v", testDiagnostic)
	}
	if !strings.Contains(testDiagnostic.Message, "missingName") {
		t.Fatalf("test diagnostic message = %q", testDiagnostic.Message)
	}
	for _, diagnostic := range diagnostics {
		if !diagnostic.IsTest {
			t.Fatalf("production diagnostic leaked into successful build: %+v", diagnostic)
		}
	}
}

func diagnosticByKind(items []Diagnostic, kind string) *Diagnostic {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

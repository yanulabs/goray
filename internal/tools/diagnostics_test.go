package tools

import (
	"context"
	"testing"
	"time"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func diagnosticsFixture() staticIndexer {
	idx := &index.Index{
		RootDir: "/repo",
		Packages: map[string]*index.PkgInfo{
			"example.com/diag/a": {},
			"example.com/diag/b": {},
		},
		Symbols: map[string]*index.Symbol{
			"example.com/diag/a.Value": {ID: "example.com/diag/a.Value"},
			"example.com/diag/b.Value": {ID: "example.com/diag/b.Value"},
			"example.com/diag/b.Other": {ID: "example.com/diag/b.Other"},
		},
		BuiltAt: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
	}
	return staticIndexer{
		idx:    idx,
		status: index.Status{IndexState: index.StateError, Ready: true, Packages: 2, Symbols: 3, BuildMS: 1200},
		diagnostics: []index.Diagnostic{
			{Package: "example.com/diag/a", File: "a/a.go", Line: 3, Col: 5, Kind: index.DiagnosticKindType, Message: "cannot use \"x\" as int"},
			{Package: "example.com/diag/a", File: "a/a_test.go", Line: 5, Col: 1, Kind: index.DiagnosticKindType, Message: "undefined: missingName", IsTest: true},
			{Package: "example.com/diag/b", File: "b/b.go", Line: 2, Col: 1, Kind: index.DiagnosticKindParse, Message: "expected ';'"},
			{Kind: index.DiagnosticKindList, Message: "no packages found"},
		},
	}
}

func TestGetDiagnosticsReturnsStructuredPayloadAndFilters(t *testing.T) {
	tools := toolMapForIndexer(t, diagnosticsFixture())
	tool := tools["get_diagnostics"]

	payload := decodePayload(t, mustRunTool(t, tool, nil))
	diagnostics := payload["diagnostics"].([]any)
	if len(diagnostics) != 4 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	counts := payload["counts_by_kind"].(map[string]any)
	if counts["list"] != float64(1) || counts["parse"] != float64(1) || counts["type"] != float64(2) || counts["total"] != float64(4) {
		t.Fatalf("counts = %+v", counts)
	}
	if payload["index_state"] != string(index.StateError) {
		t.Fatalf("index_state = %v", payload["index_state"])
	}
	snapshot := payload["last_healthy_snapshot"].(map[string]any)
	if snapshot["available"] != true || snapshot["packages"] != float64(2) || snapshot["symbols"] != float64(3) || snapshot["built_at"] == "" {
		t.Fatalf("last healthy snapshot = %+v", snapshot)
	}
	first := diagnostics[0].(map[string]any)
	for _, field := range []string{"package", "file", "line", "col", "kind", "message", "is_test"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("diagnostic missing %q: %+v", field, first)
		}
	}
	if first["line"] != float64(3) || first["col"] != float64(5) || first["is_test"] != false {
		t.Fatalf("first diagnostic = %+v", first)
	}

	payload = decodePayload(t, mustRunTool(t, tool, map[string]any{"include_tests": false}))
	if got := len(payload["diagnostics"].([]any)); got != 3 {
		t.Fatalf("include_tests=false diagnostics = %d", got)
	}

	payload = decodePayload(t, mustRunTool(t, tool, map[string]any{"package": "a"}))
	diagnostics = payload["diagnostics"].([]any)
	if len(diagnostics) != 2 {
		t.Fatalf("package filter diagnostics = %+v", diagnostics)
	}
	for _, item := range diagnostics {
		if item.(map[string]any)["package"] != "example.com/diag/a" {
			t.Fatalf("package filter leaked %+v", item)
		}
	}

	payload = decodePayload(t, mustRunTool(t, tool, map[string]any{"package": "./b", "include_tests": false}))
	diagnostics = payload["diagnostics"].([]any)
	if len(diagnostics) != 1 || diagnostics[0].(map[string]any)["package"] != "example.com/diag/b" {
		t.Fatalf("relative package filter = %+v", diagnostics)
	}

	payload = decodePayload(t, mustRunTool(t, tool, map[string]any{"package": "missing"}))
	if got := payload["diagnostics"].([]any); len(got) != 0 {
		t.Fatalf("missing package filter = %+v", got)
	}
}

func TestGetDiagnosticsEmptyAndHealthySnapshot(t *testing.T) {
	idx := &index.Index{
		RootDir:   "/repo",
		Packages:  map[string]*index.PkgInfo{},
		Symbols:   map[string]*index.Symbol{"x": {ID: "x"}},
		BuildTags: []string{"integration"},
	}
	tools := toolMapForIndexer(t, staticIndexer{idx: idx})
	payload := decodePayload(t, mustRunTool(t, tools["get_diagnostics"], nil))
	if got := payload["diagnostics"].([]any); len(got) != 0 {
		t.Fatalf("diagnostics = %+v", got)
	}
	counts := payload["counts_by_kind"].(map[string]any)
	if counts["total"] != float64(0) {
		t.Fatalf("counts = %+v", counts)
	}
	snapshot := payload["last_healthy_snapshot"].(map[string]any)
	if snapshot["available"] != true || snapshot["symbols"] != float64(1) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func toolMapForIndexer(t *testing.T, indexer Indexer) map[string]mcp.Tool {
	t.Helper()
	tools := New(indexer, render.NewRegistry(), handlerSummaries{})
	out := make(map[string]mcp.Tool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}

func mustRunTool(t *testing.T, tool mcp.Tool, args map[string]any) string {
	t.Helper()
	text, failed := tool.Run(context.Background(), args)
	if failed {
		t.Fatalf("%s failed: %s", tool.Name, text)
	}
	return text
}

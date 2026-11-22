package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func explainDependencyTool(t *testing.T) mcp.Tool {
	t.Helper()
	idx, err := index.Build("../index/testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range New(staticIndexer{idx: idx}, render.NewRegistry(), nil) {
		if tool.Name == "explain_dependency" {
			return tool
		}
	}
	t.Fatal("explain_dependency not registered")
	return mcp.Tool{}
}

func TestExplainDependencyTool(t *testing.T) {
	tool := explainDependencyTool(t)
	text, failed := tool.Run(context.Background(), map[string]any{
		"from": "bookinggw",
		"to":   "payments",
	})
	if failed {
		t.Fatalf("explain_dependency failed: %s", text)
	}
	var out struct {
		FromKind string `json:"from_kind"`
		ToKind   string `json:"to_kind"`
		Edges    []struct {
			FromPkg string `json:"from_pkg"`
			ToPkg   string `json:"to_pkg"`
			Files   []struct {
				File string `json:"file"`
				Line int    `json:"line"`
			} `json:"files"`
			Symbols []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"symbols"`
		} `json:"edges"`
		Summary struct {
			Files         int `json:"files"`
			TargetSymbols int `json:"target_symbols"`
			UsageSites    int `json:"usage_sites"`
		} `json:"summary"`
		IndexState string `json:"index_state"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.FromKind != "module" || out.ToKind != "module" {
		t.Fatalf("kinds = %q -> %q", out.FromKind, out.ToKind)
	}
	if len(out.Edges) != 1 || len(out.Edges[0].Files) == 0 || out.Edges[0].Files[0].Line == 0 {
		t.Fatalf("edges = %+v", out.Edges)
	}
	if len(out.Edges[0].Symbols) == 0 || out.Summary.UsageSites == 0 {
		t.Fatalf("symbols/summary = %+v / %+v", out.Edges[0].Symbols, out.Summary)
	}
	for _, symbol := range out.Edges[0].Symbols {
		if symbol.Kind == "" {
			t.Fatalf("symbol kind missing: %+v", symbol)
		}
	}
	if out.IndexState != "ready" {
		t.Fatalf("index_state = %q", out.IndexState)
	}

	missing, failed := tool.Run(context.Background(), map[string]any{"from": "bookinggw", "to": "missing"})
	if !failed {
		t.Fatalf("expected failure for unknown target, got %s", missing)
	}
}

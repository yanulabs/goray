package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

type handlerSummaries struct{}

func (handlerSummaries) Enrich(context.Context, string) (json.RawMessage, cache.SyncState, bool, error) {
	return nil, cache.StateMissing, false, nil
}

func (handlerSummaries) ListStale(context.Context, cache.StaleFilter) ([]cache.StaleSymbol, error) {
	return []cache.StaleSymbol{}, nil
}

func (handlerSummaries) SummaryContext(context.Context, string, *index.Index) (cache.SummaryContext, error) {
	return cache.SummaryContext{
		SyncState:     cache.StateMissing,
		PromptVersion: cache.PromptVersion,
		Schema:        cache.SummarySchema("func"),
	}, nil
}

func (handlerSummaries) UpsertSummaries(_ context.Context, items []cache.SummaryInput) ([]cache.UpsertResult, error) {
	results := make([]cache.UpsertResult, 0, len(items))
	for _, item := range items {
		results = append(results, cache.UpsertResult{SymbolID: item.SymbolID, Status: "updated"})
	}
	return results, nil
}

func (handlerSummaries) Coverage(context.Context, string) (cache.Coverage, error) {
	return cache.Coverage{PromptVersion: cache.PromptVersion, Totals: cache.CoverageCounts{Eligible: 1, Synced: 1, CoveragePct: 100}}, nil
}

func (handlerSummaries) Path() string        { return ":memory:" }
func (handlerSummaries) SummaryPath() string { return ":memory:" }

func handlerIndex(t *testing.T) *index.Index {
	t.Helper()
	idx := buildProjectionFixture(t)
	if len(idx.SortedModuleNames()) < 2 {
		t.Fatalf("fixture modules = %v", idx.SortedModuleNames())
	}
	return idx
}

func canonicalName(t *testing.T, idx *index.Index, name string) string {
	t.Helper()
	for _, sym := range idx.Symbols {
		if sym.Name == name && sym.CanonicalID() != "" {
			return sym.CanonicalID()
		}
	}
	t.Fatalf("symbol %q not found", name)
	return ""
}

func toolMap(t *testing.T, idx *index.Index) map[string]mcp.Tool {
	t.Helper()
	tools := New(staticIndexer{idx: idx}, render.NewRegistry(), handlerSummaries{})
	out := make(map[string]mcp.Tool, len(tools))
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}

func TestEveryRegisteredToolRuns(t *testing.T) {
	idx := handlerIndex(t)
	modules := idx.SortedModuleNames()
	normal := canonicalName(t, idx, "Normal")
	api := canonicalName(t, idx, "API")
	alphaPkg := "example.com/proj/modules/alpha"
	betaPkg := "example.com/proj/modules/beta"

	cases := []struct {
		name string
		args map[string]any
	}{
		{"project_info", nil},
		{"get_codebase_map", map[string]any{"format": "json"}},
		{"get_module_overview", map[string]any{"module": modules[0], "format": "json"}},
		{"get_dependency_graph", map[string]any{"package": alphaPkg, "direction": "both", "format": "json"}},
		{"explain_dependency", map[string]any{"from": betaPkg, "to": alphaPkg}},
		{"lookup_symbol", map[string]any{"symbol": "Normal"}},
		{"get_api_surface", map[string]any{"package": alphaPkg, "format": "json"}},
		{"get_references", map[string]any{"symbol_id": normal}},
		{"get_call_graph", map[string]any{"symbol_id": normal, "direction": "both"}},
		{"find_implementations", map[string]any{"symbol_id": api}},
		{"get_impact_analysis", map[string]any{"symbol_id": normal}},
		{"get_dead_code", nil},
		{"get_hotspots", nil},
		{"get_function_body", map[string]any{"symbol_id": normal}},
		{"list_stale_summaries", nil},
		{"get_summary_context", map[string]any{"symbol_id": normal}},
		{"upsert_summaries", map[string]any{"items": []any{map[string]any{
			"symbol_id":          normal,
			"summary":            map[string]any{"intent": "test"},
			"expected_self_hash": "",
			"expected_dep_hash":  "",
		}}}},
		{"get_summary_coverage", nil},
		{"get_diagnostics", nil},
		{"get_architecture_diff", nil},
		{"run_tests", map[string]any{"packages": "./..."}},
		{"run_lint", map[string]any{"packages": "./..."}},
		{"reindex", nil},
	}

	registered := toolMap(t, idx)
	if len(cases) != len(registered) {
		t.Fatalf("cases = %d, tools = %d", len(cases), len(registered))
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tool, ok := registered[test.name]
			if !ok {
				t.Fatalf("tool %q is not registered", test.name)
			}
			text, failed := tool.Run(context.Background(), test.args)
			if failed {
				t.Fatalf("%s failed: %s", test.name, text)
			}
			if text == "" {
				t.Fatalf("%s returned an empty payload", test.name)
			}
		})
	}
}

func TestCompactMarkdownAndFullProjections(t *testing.T) {
	idx := handlerIndex(t)
	modules := idx.SortedModuleNames()
	tools := toolMap(t, idx)

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"get_codebase_map", nil, "# Architecture Map"},
		{"get_codebase_map", map[string]any{"summary_only": true}, "## Payload"},
		{"get_codebase_map", map[string]any{"detail": "full", "format": "json"}, `"module_path"`},
		{"get_module_overview", map[string]any{"module": modules[0]}, "# Module"},
		{"get_module_overview", map[string]any{"module": modules[0], "detail": "full", "format": "json"}, `"subpackages"`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			text, failed := tools[test.name].Run(context.Background(), test.args)
			if failed {
				t.Fatalf("%s failed: %s", test.name, text)
			}
			if !strings.Contains(text, test.want) {
				t.Fatalf("%s = %q, want %q", test.name, text, test.want)
			}
		})
	}
}

func TestHandlerValidationErrors(t *testing.T) {
	idx := handlerIndex(t)
	tools := toolMap(t, idx)
	ids := make([]string, 26)
	for i := range ids {
		ids[i] = "sym:example.com/proj/modules/alpha.Normal"
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"get_summary_context", nil},
		{"get_summary_context", map[string]any{"symbol_ids": ids}},
		{"upsert_summaries", map[string]any{"items": []any{}}},
		{"get_function_body", nil},
		{"get_references", nil},
		{"get_call_graph", nil},
		{"find_implementations", nil},
		{"get_impact_analysis", nil},
		{"explain_dependency", map[string]any{"from": "example.com/proj/modules/alpha"}},
		{"get_api_surface", nil},
		{"get_module_overview", nil},
		{"get_module_overview", map[string]any{"module": "missing"}},
		{"get_module_overview", map[string]any{"module": "example.com/proj/modules/alpha", "detail": "nope"}},
		{"get_codebase_map", map[string]any{"detail": "nope"}},
		{"get_codebase_map", map[string]any{"sort_by": "nope"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tool, ok := tools[test.name]
			if !ok {
				t.Fatalf("tool %q is not registered", test.name)
			}
			text, failed := tool.Run(context.Background(), test.args)
			if !failed {
				t.Fatalf("%s did not fail: %s", test.name, text)
			}
			if text == "" {
				t.Fatalf("%s returned an empty error", test.name)
			}
		})
	}
}

func TestRunTestsAndLintUseIndexRoot(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/commands\n\ngo 1.26\n",
		"pkg/pkg.go":      "package pkg\n\nfunc Value() int { return 1 }\n",
		"pkg/pkg_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n",
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
	idx := &index.Index{RootDir: dir}
	tools := toolMap(t, idx)
	text, failed := tools["run_tests"].Run(context.Background(), map[string]any{"packages": "./..."})
	if failed {
		t.Fatalf("run_tests failed: %s", text)
	}
	text, failed = tools["run_lint"].Run(context.Background(), map[string]any{"packages": "./..."})
	if failed {
		t.Fatalf("run_lint failed: %s", text)
	}
}

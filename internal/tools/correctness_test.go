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

type recordingSummaries struct {
	summaries map[string]json.RawMessage
	coverage  string
	enrich    []string
}

func (r *recordingSummaries) Enrich(_ context.Context, id string) (json.RawMessage, cache.SyncState, bool, error) {
	r.enrich = append(r.enrich, id)
	if summary, ok := r.summaries[id]; ok {
		return summary, cache.StateSynced, false, nil
	}
	return nil, cache.StateMissing, false, nil
}

func (r *recordingSummaries) ListStale(context.Context, cache.StaleFilter) ([]cache.StaleSymbol, error) {
	return nil, nil
}

func (r *recordingSummaries) SummaryContext(context.Context, string, *index.Index) (cache.SummaryContext, error) {
	return cache.SummaryContext{}, nil
}

func (r *recordingSummaries) UpsertSummaries(context.Context, []cache.SummaryInput) ([]cache.UpsertResult, error) {
	return nil, nil
}

func (r *recordingSummaries) Coverage(_ context.Context, module string) (cache.Coverage, error) {
	r.coverage = module
	return cache.Coverage{PromptVersion: cache.PromptVersion}, nil
}

func (r *recordingSummaries) Path() string        { return ":memory:" }
func (r *recordingSummaries) SummaryPath() string { return ":memory:" }

func TestCompactCodebaseReportsUntrackedTotals(t *testing.T) {
	idx := buildProjectionFixture(t)
	idx.Packages["example.com/proj/modules/alpha"].UntrackedFiles = 2
	idx.Packages["example.com/proj/modules/beta"].UntrackedFiles = 1
	tool := projectionTool(t, idx, "get_codebase_map", nil)

	text, failed := tool.Run(context.Background(), map[string]any{"format": "json", "summary_only": true})
	if failed {
		t.Fatal(text)
	}
	var out compactCodebase
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.UntrackedFiles != 3 {
		t.Fatalf("untracked_files = %d, want 3", out.UntrackedFiles)
	}

	text, failed = tool.Run(context.Background(), map[string]any{"format": "markdown", "summary_only": true})
	if failed {
		t.Fatal(text)
	}
	if !strings.Contains(text, "Untracked files:** 3") {
		t.Fatalf("compact markdown missing untracked total:\n%s", text)
	}
}

func TestCompactProjectionsIncludeCachedModuleSummaries(t *testing.T) {
	idx := buildProjectionFixture(t)
	summary := json.RawMessage(`{"intent":"alpha owns the public API"}`)
	summaries := &recordingSummaries{summaries: map[string]json.RawMessage{"mod:alpha": summary}}
	tools := toolMapWithSummaries(t, idx, summaries)

	for _, format := range []string{"json", "markdown"} {
		t.Run("codebase/"+format, func(t *testing.T) {
			text, failed := tools["get_codebase_map"].Run(context.Background(), map[string]any{
				"format": format, "top_modules": float64(1), "summary_only": true,
			})
			if failed {
				t.Fatal(text)
			}
			if !strings.Contains(text, "alpha owns the public API") {
				t.Fatalf("compact codebase summary missing:\n%s", text)
			}
			if format == "markdown" && !strings.Contains(text, "Cached Module Summaries") {
				t.Fatalf("compact markdown missing summary heading:\n%s", text)
			}
			if format == "json" {
				var out compactCodebase
				if err := json.Unmarshal([]byte(text), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Modules) != 1 || !strings.Contains(string(out.Modules[0].Summary), "alpha owns the public API") || out.Modules[0].SyncState != string(cache.StateSynced) {
					t.Fatalf("module summary = %+v", out.Modules)
				}
			}
		})
	}

	for _, format := range []string{"json", "markdown"} {
		t.Run("module/"+format, func(t *testing.T) {
			text, failed := tools["get_module_overview"].Run(context.Background(), map[string]any{
				"module": "mod:alpha", "format": format,
			})
			if failed {
				t.Fatal(text)
			}
			if !strings.Contains(text, "alpha owns the public API") {
				t.Fatalf("compact module summary missing:\n%s", text)
			}
			if format == "markdown" && !strings.Contains(text, "Cached Summary") {
				t.Fatalf("compact module markdown missing summary heading:\n%s", text)
			}
			if format == "json" {
				var out compactModuleOverview
				if err := json.Unmarshal([]byte(text), &out); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(out.Summary), "alpha owns the public API") || out.SyncState != string(cache.StateSynced) {
					t.Fatalf("overview summary = %s state=%s", out.Summary, out.SyncState)
				}
			}
		})
	}
}

func toolMapWithSummaries(t *testing.T, idx *index.Index, summaries Summaries) map[string]mcp.Tool {
	t.Helper()
	out := map[string]mcp.Tool{}
	for _, tool := range New(staticIndexer{idx: idx}, render.NewRegistry(), summaries) {
		out[tool.Name] = tool
	}
	return out
}

func TestSummaryCoverageAcceptsModPrefix(t *testing.T) {
	summaries := &recordingSummaries{}
	tools := toolMapWithSummaries(t, &index.Index{}, summaries)
	for _, module := range []any{"mod:alpha", "alpha"} {
		text, failed := tools["get_summary_coverage"].Run(context.Background(), map[string]any{"module": module})
		if failed {
			t.Fatal(text)
		}
		if summaries.coverage != "alpha" {
			t.Fatalf("coverage module = %q for input %v", summaries.coverage, module)
		}
	}
}

func TestAPISurfacePaginatesBeforeEnrich(t *testing.T) {
	idx := buildProjectionFixture(t)
	summaries := &recordingSummaries{}
	tools := toolMapWithSummaries(t, idx, summaries)
	text, failed := tools["get_api_surface"].Run(context.Background(), map[string]any{
		"package": "example.com/proj/modules/alpha", "format": "json", "limit": float64(1),
	})
	if failed {
		t.Fatal(text)
	}
	if len(summaries.enrich) != 1 {
		t.Fatalf("enrich calls = %d (%v), want 1", len(summaries.enrich), summaries.enrich)
	}
	var out struct {
		Symbols map[string][]apiSurfaceItem `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, items := range out.Symbols {
		total += len(items)
	}
	if total != 1 {
		t.Fatalf("returned symbols = %d, want 1", total)
	}
}

func TestEnclosingSummaryQueriesFirstCandidateOnce(t *testing.T) {
	summaries := &recordingSummaries{}
	toolset := &toolset{summaries: summaries}
	sym := &index.Symbol{PkgPath: "example.com/proj/modules/alpha", ModuleArea: "alpha"}
	id, summary, state := toolset.enclosingSummary(context.Background(), sym)
	if id != "pkg:example.com/proj/modules/alpha" || len(summary) != 0 || state != string(cache.StateMissing) {
		t.Fatalf("enclosing summary = %q %s %s", id, summary, state)
	}
	if len(summaries.enrich) != 2 || summaries.enrich[0] != "pkg:example.com/proj/modules/alpha" || summaries.enrich[1] != "mod:alpha" {
		t.Fatalf("enrich calls = %v", summaries.enrich)
	}
}

func TestRunCommandPackagesValidationAndBuildTags(t *testing.T) {
	tests := []struct {
		name      string
		packages  any
		want      []string
		wantError string
	}{
		{name: "omitted defaults", want: []string{"./..."}},
		{name: "whitespace separated", packages: " ./a  ./b ", want: []string{"./a", "./b"}},
		{name: "array with whitespace", packages: []any{"./a", "./b ./c"}, want: []string{"./a", "./b", "./c"}},
		{name: "string slice", packages: []string{"./a", "./b"}, want: []string{"./a", "./b"}},
		{name: "empty array defaults", packages: []any{}, want: []string{"./..."}},
		{name: "flag injection string", packages: "./a -exec=id", wantError: "must not start with '-'"},
		{name: "flag injection array", packages: []any{"-run=Test"}, wantError: "must not start with '-'"},
		{name: "wrong item type", packages: []any{float64(1)}, wantError: "must be a non-empty string"},
		{name: "wrong type", packages: float64(1), wantError: "string or an array"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"run": "TestThing"}
			if test.packages != nil {
				args["packages"] = test.packages
			}
			got, err := testCommandArgs(args, []string{"integration", "unit"})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(got, " ")
			for _, want := range []string{"test -race -count=1", "-run TestThing", "-tags=integration,unit"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("args = %v, missing %q", got, want)
				}
			}
			for _, pkg := range test.want {
				if !strings.Contains(joined, pkg) {
					t.Fatalf("args = %v, missing package %q", got, pkg)
				}
			}
		})
	}
}

func TestRunTestsForwardsServerBuildTags(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/tagged\n\ngo 1.26\n",
		"pkg/pkg.go":    "package pkg\n\nfunc Base() int { return 1 }\n",
		"pkg/tagged.go": "//go:build integration\n\npackage pkg\n\nfunc Tagged() int { return 2 }\n",
		"pkg/pkg_test.go": `package pkg

import "testing"

func TestTagged(t *testing.T) {
	if Tagged() != 2 || Base() != 1 {
		t.Fatal("unexpected values")
	}
}
`,
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
	tools := New(staticIndexer{
		idx:    &index.Index{RootDir: dir},
		status: index.Status{BuildTags: []string{"integration"}},
	}, render.NewRegistry(), nil)
	var runTool func(context.Context, map[string]any) (string, bool)
	for _, tool := range tools {
		if tool.Name == "run_tests" {
			runTool = tool.Run
		}
	}
	if runTool == nil {
		t.Fatal("run_tests not registered")
	}
	text, failed := runTool(context.Background(), map[string]any{"packages": []any{"./pkg"}})
	if failed {
		t.Fatalf("run_tests did not forward build tags: %s", text)
	}
}

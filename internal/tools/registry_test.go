package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

type fakeIndexer struct{}

func (fakeIndexer) Wait(context.Context) (*index.Index, error) { return nil, errors.New("not ready") }
func (fakeIndexer) Status() index.Status                       { return index.Status{RootDir: "."} }
func (fakeIndexer) Root() string                               { return "." }
func (fakeIndexer) Rebuild() error                             { return nil }

func TestRegistry(t *testing.T) {
	list := New(fakeIndexer{}, render.NewRegistry(), nil)
	if len(list) != 21 {
		t.Fatalf("tools = %d, want 21", len(list))
	}

	seen := map[string]bool{}
	for _, tool := range list {
		if tool.Name == "" || tool.Description == "" || tool.Run == nil {
			t.Fatalf("incomplete tool: %+v", tool)
		}
		if seen[tool.Name] {
			t.Fatalf("duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.InputSchema["type"] != "object" {
			t.Fatalf("%s has invalid schema: %v", tool.Name, tool.InputSchema)
		}
	}

	text, failed := list[0].Run(context.Background(), nil)
	if failed {
		t.Fatalf("project_info failed: %s", text)
	}
	var info struct {
		Tools int `json:"tools"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil || info.Tools != len(list) {
		t.Fatalf("project_info = %s (%v)", text, err)
	}
}

func TestSkillDocCoversAllRegisteredTools(t *testing.T) {
	path := filepath.Join("..", "..", ".opencode", "skills", "goray", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(data)
	list := New(fakeIndexer{}, render.NewRegistry(), nil)
	for _, tool := range list {
		if !strings.Contains(doc, "`"+tool.Name+"`") {
			t.Errorf("SKILL.md does not document registered tool %q", tool.Name)
		}
	}
	// The documentation must name every critical parameter used by the
	// dependency and hotspot tools; silent schema drift caused real failures.
	for _, required := range []string{
		"`package`", "`direction`", "`depth`", "`format`",
		"`include_tests`", "`from`", "`to`",
		"`include_interface_dispatch`", "`via_interface`",
		"`test_only`", "`untested_only`", "`sort_by`",
		"`symbol_id`", "`detail`", "`summary_only`", "`top_modules`",
		"`include_generated`", "`offset`", "`generated_omitted`",
	} {
		if !strings.Contains(doc, required) {
			t.Errorf("SKILL.md is missing critical parameter %s", required)
		}
	}
}

type fakeSummaries struct {
	path        string
	summaryPath string
}

func (f fakeSummaries) Enrich(context.Context, string) (json.RawMessage, cache.SyncState, bool, error) {
	return nil, "", false, nil
}

func (f fakeSummaries) ListStale(context.Context, cache.StaleFilter) ([]cache.StaleSymbol, error) {
	return nil, nil
}

func (f fakeSummaries) SummaryContext(context.Context, string, *index.Index) (cache.SummaryContext, error) {
	return cache.SummaryContext{}, nil
}

func (f fakeSummaries) UpsertSummaries(context.Context, []cache.SummaryInput) ([]cache.UpsertResult, error) {
	return nil, nil
}

func (f fakeSummaries) Coverage(context.Context, string) (cache.Coverage, error) {
	return cache.Coverage{}, nil
}

func (f fakeSummaries) Path() string { return f.path }

func (f fakeSummaries) SummaryPath() string {
	if f.summaryPath != "" {
		return f.summaryPath
	}
	return f.path
}

func TestProjectInfoIncludesDBPath(t *testing.T) {
	list := New(fakeIndexer{}, render.NewRegistry(), fakeSummaries{path: ":memory:"})
	text, failed := list[0].Run(context.Background(), nil)
	if failed {
		t.Fatalf("project_info failed: %s", text)
	}
	var info struct {
		DBPath        string `json:"db_path"`
		SummaryDBPath string `json:"summary_db_path"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatal(err)
	}
	if info.DBPath != ":memory:" {
		t.Fatalf("db_path = %q", info.DBPath)
	}
	if info.SummaryDBPath != ":memory:" {
		t.Fatalf("summary_db_path = %q", info.SummaryDBPath)
	}
}

func TestProjectInfoIncludesSplitSummaryDBPath(t *testing.T) {
	list := New(fakeIndexer{}, render.NewRegistry(), fakeSummaries{path: "/tmp/work.db", summaryPath: "/tmp/summary.db"})
	text, failed := list[0].Run(context.Background(), nil)
	if failed {
		t.Fatalf("project_info failed: %s", text)
	}
	var info struct {
		DBPath        string `json:"db_path"`
		SummaryDBPath string `json:"summary_db_path"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatal(err)
	}
	if info.DBPath != "/tmp/work.db" || info.SummaryDBPath != "/tmp/summary.db" {
		t.Fatalf("paths = %+v", info)
	}
}

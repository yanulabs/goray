package render

import (
	"slices"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func testIndex() *index.Index {
	return &index.Index{
		ModulePath: "example.com/app",
		Packages: map[string]*index.PkgInfo{
			"example.com/app/api": {PkgPath: "example.com/app/api", RelPath: "api", ModuleArea: "api", ProdLOC: 5000, Exported: 90},
			"example.com/app/db":  {PkgPath: "example.com/app/db", RelPath: "db", ModuleArea: "db"},
		},
		Symbols: map[string]*index.Symbol{
			"example.com/app/api.F": {ID: "example.com/app/api.F", Name: "F", PkgPath: "example.com/app/api"},
		},
		Modules: map[string]*index.ModSummary{
			"api": {
				Name: "api", ProdLOC: 5000, RootExported: 90, PortsCalls: 2, Packages: 1,
				OutModules: map[string][]string{"db": {"Open"}},
			},
			"db": {Name: "db", Packages: 1},
		},
		LargeFiles: []index.FileMetric{{Path: "big.go", LOC: 500}},
	}
}

func TestRegistryRendering(t *testing.T) {
	idx := testIndex()
	registry := NewRegistry()
	graph := index.Graph{
		Root:  "api",
		Nodes: 2,
		Edges: []index.Edge{{From: "api", To: "db"}},
	}

	if !slices.Equal(registry.CodebaseMapFormats(), []string{"dot", "json", "markdown", "mermaid"}) {
		t.Fatalf("codebase formats = %v", registry.CodebaseMapFormats())
	}
	if !slices.Equal(registry.ModuleFormats(), []string{"json", "markdown"}) {
		t.Fatalf("module formats = %v", registry.ModuleFormats())
	}
	if !slices.Equal(registry.GraphFormats(), []string{"dot", "json", "mermaid"}) {
		t.Fatalf("graph formats = %v", registry.GraphFormats())
	}

	md, ok := registry.RenderCodebaseMap("markdown", idx)
	if !ok {
		t.Fatal("markdown codebase map missing")
	}
	for _, want := range []string{
		"example.com/app",
		"| `api` |",
		"api -->|1 sym| db",
		"`Open`",
		"big.go",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}

	dot, ok := registry.RenderCodebaseMap("dot", idx)
	if !ok || !strings.Contains(dot, "api -> db") || !strings.Contains(dot, "5.0k prod") {
		t.Fatalf("unexpected DOT:\n%s", dot)
	}

	mermaid, ok := registry.RenderCodebaseMap("mermaid", idx)
	if !ok || !strings.Contains(mermaid, "api -->|1 sym| db") {
		t.Fatalf("unexpected Mermaid:\n%s", mermaid)
	}

	jsonMap, ok := registry.RenderCodebaseMap("json", idx)
	if !ok || !strings.Contains(jsonMap, `"top_complex"`) || !strings.Contains(jsonMap, "big.go") {
		t.Fatalf("unexpected JSON map:\n%s", jsonMap)
	}

	moduleMD, ok := registry.RenderModule("markdown", idx, idx.Modules["api"])
	if !ok || !strings.Contains(moduleMD, "# Module `api`") {
		t.Fatalf("unexpected module markdown:\n%s", moduleMD)
	}
	moduleJSON, ok := registry.RenderModule("json", idx, idx.Modules["api"])
	if !ok || !strings.Contains(moduleJSON, `"subpackages"`) {
		t.Fatalf("unexpected module JSON:\n%s", moduleJSON)
	}

	for _, format := range []string{"dot", "json", "mermaid"} {
		out, ok := registry.RenderGraph(format, graph)
		if !ok || !strings.Contains(out, "api") || !strings.Contains(out, "db") {
			t.Fatalf("unexpected graph %s:\n%s", format, out)
		}
	}

	if _, ok := registry.RenderGraph("markdown", graph); ok {
		t.Fatal("markdown must not claim graph capability")
	}
	if _, ok := registry.RenderModule("dot", idx, idx.Modules["api"]); ok {
		t.Fatal("dot must not claim module capability")
	}
}

type customRenderer struct{ name string }

func (c customRenderer) Name() string { return c.name }

func (c customRenderer) RenderGraph(index.Graph) string { return "custom graph" }

type jsonOverride struct{}

func (jsonOverride) Name() string { return "json" }

func (jsonOverride) RenderGraph(index.Graph) string { return "json override" }

func TestRegistryExtension(t *testing.T) {
	registry := NewRegistry(customRenderer{name: "custom"})
	if !slices.Contains(registry.GraphFormats(), "custom") {
		t.Fatalf("graph formats = %v", registry.GraphFormats())
	}
	if slices.Contains(registry.CodebaseMapFormats(), "custom") {
		t.Fatal("custom renderer must not gain codebase capability")
	}
	if out, ok := registry.RenderGraph("custom", index.Graph{}); !ok || out != "custom graph" {
		t.Fatalf("custom graph = %q, %v", out, ok)
	}

	override := NewRegistry(jsonOverride{})
	if slices.Contains(override.CodebaseMapFormats(), "json") {
		t.Fatal("re-registering json should replace its codebase capability")
	}
	if !slices.Contains(override.GraphFormats(), "json") {
		t.Fatalf("override graph formats = %v", override.GraphFormats())
	}
	if out, ok := override.RenderGraph("json", index.Graph{}); !ok || out != "json override" {
		t.Fatalf("override graph = %q, %v", out, ok)
	}
}

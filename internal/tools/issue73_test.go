package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func issue73Index(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/issue73\n\ngo 1.23\n",
		"pub/pub.go": `package pub

func onlyTested() {}
func unused()     {}
func Handler()    {}
func Register()   { _ = Handler }
`,
		"pub/pub_test.go": `package pub

import "testing"

func TestOnly(t *testing.T) { onlyTested() }
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
	idx, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func issue73Tool(t *testing.T, idx *index.Index, name string) mcp.Tool {
	t.Helper()
	for _, tool := range New(staticIndexer{idx: idx}, render.NewRegistry(), nil) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not registered", name)
	return mcp.Tool{}
}

func TestDeadCodeToolIgnoreTestRefs(t *testing.T) {
	idx := issue73Index(t)
	tool := issue73Tool(t, idx, "get_dead_code")
	schema := render.JSON(tool.InputSchema)
	if !strings.Contains(schema, "ignore_test_refs") {
		t.Fatalf("schema does not expose ignore_test_refs: %s", schema)
	}

	out := runToolJSON(t, tool, nil)
	if names := deadCandidateNames(t, out); names["onlyTested"] {
		t.Fatalf("default dead code included a test-referenced function: %v", names)
	}
	out = runToolJSON(t, tool, map[string]any{"ignore_test_refs": true})
	names := deadCandidateNames(t, out)
	if !names["onlyTested"] {
		t.Fatalf("ignore_test_refs did not report onlyTested: %v", names)
	}
	if !names["unused"] {
		t.Fatalf("ignore_test_refs lost unused: %v", names)
	}
}

func TestCallGraphToolExposesFuncValueEdges(t *testing.T) {
	idx := issue73Index(t)
	tool := issue73Tool(t, idx, "get_call_graph")
	out := runToolJSON(t, tool, map[string]any{
		"symbol_id": "example.com/issue73/pub.Register",
		"direction": "callees",
		"depth":     float64(1),
	})
	edges, _ := out["edges"].([]any)
	found := false
	for _, raw := range edges {
		edge, _ := raw.(map[string]any)
		if edge["to"] == "example.com/issue73/pub.Handler" && edge["edge_kind"] == "func_value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("func_value edge missing from call graph: %v", edges)
	}
}

func deadCandidateNames(t *testing.T, out map[string]any) map[string]bool {
	t.Helper()
	raw, _ := out["candidates"].([]any)
	names := map[string]bool{}
	for _, item := range raw {
		candidate, _ := item.(map[string]any)
		name, _ := candidate["name"].(string)
		names[name] = true
	}
	return names
}

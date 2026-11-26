package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func buildCallPathToolIndex(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/toolpath\n\ngo 1.23\n",
		"app/app.go": `package app

type Store interface {
	Save(amount int) error
}

type DB struct{}

func (DB) Save(amount int) error { return nil }

type Service struct {
	store Store
}

func (s *Service) Run(amount int) error { return s.store.Save(amount) }

func Entry() error { return (&Service{store: DB{}}).Run(1) }

func Target() {}

func Mid() { Target() }

func Top() { Mid() }

func Callback() {}

func Register() { _ = Callback }
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

func callPathTool(t *testing.T, idx *index.Index) mcp.Tool {
	t.Helper()
	for _, tool := range New(staticIndexer{idx: idx}, render.NewRegistry(), nil) {
		if tool.Name == "find_call_path" {
			return tool
		}
	}
	t.Fatal("find_call_path not registered")
	return mcp.Tool{}
}

type callPathPayload struct {
	From struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		File string `json:"file"`
		Line int    `json:"line"`
	} `json:"from"`
	To struct {
		ID string `json:"id"`
	} `json:"to"`
	Found bool `json:"found"`
	Paths []struct {
		Depth int `json:"depth"`
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
			File string `json:"file"`
			Line int    `json:"line"`
		} `json:"nodes"`
		Hops []struct {
			From         struct{ ID string } `json:"from"`
			To           struct{ ID string } `json:"to"`
			EdgeKind     string              `json:"edge_kind"`
			ViaInterface string              `json:"via_interface"`
		} `json:"hops"`
	} `json:"paths"`
	MaxDepth                 int    `json:"max_depth"`
	MaxPaths                 int    `json:"max_paths"`
	IncludeInterfaceDispatch bool   `json:"include_interface_dispatch"`
	ReversePathExists        bool   `json:"reverse_path_exists"`
	ReversePathHint          string `json:"reverse_path_hint"`
	IndexState               string `json:"index_state"`
}

func decodeCallPath(t *testing.T, tool mcp.Tool, args map[string]any) callPathPayload {
	t.Helper()
	text, failed := tool.Run(t.Context(), args)
	if failed {
		t.Fatalf("find_call_path failed: %s", text)
	}
	var out callPathPayload
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("invalid JSON %q: %v", text, err)
	}
	return out
}

func TestFindCallPathToolDirectAndFuncValue(t *testing.T) {
	tool := callPathTool(t, buildCallPathToolIndex(t))

	out := decodeCallPath(t, tool, map[string]any{
		"from": "example.com/toolpath/app.Top",
		"to":   "example.com/toolpath/app.Target",
	})
	if !out.Found || len(out.Paths) != 1 || out.Paths[0].Depth != 2 {
		t.Fatalf("direct payload = %+v", out)
	}
	if out.From.ID != "example.com/toolpath/app.Top" || out.From.Kind != "func" || out.From.File == "" || out.From.Line == 0 {
		t.Fatalf("from descriptor = %+v", out.From)
	}
	if out.MaxDepth != 8 || out.MaxPaths != 3 || !out.IncludeInterfaceDispatch {
		t.Fatalf("defaults = %+v", out)
	}
	if out.IndexState != "ready" {
		t.Fatalf("index_state = %q", out.IndexState)
	}
	path := out.Paths[0]
	if len(path.Nodes) != 3 || len(path.Hops) != 2 {
		t.Fatalf("path = %+v", path)
	}
	if path.Nodes[0].Name != "Top" || path.Nodes[1].Name != "Mid" || path.Nodes[2].Name != "Target" {
		t.Fatalf("nodes = %+v", path.Nodes)
	}
	if path.Hops[0].EdgeKind != "direct" || path.Hops[1].EdgeKind != "direct" {
		t.Fatalf("hops = %+v", path.Hops)
	}

	out = decodeCallPath(t, tool, map[string]any{
		"from": "example.com/toolpath/app.Register",
		"to":   "example.com/toolpath/app.Callback",
	})
	if !out.Found || len(out.Paths) != 1 || out.Paths[0].Hops[0].EdgeKind != "func_value" {
		t.Fatalf("func_value payload = %+v", out)
	}
}

func TestFindCallPathToolInterfaceDispatchToggle(t *testing.T) {
	tool := callPathTool(t, buildCallPathToolIndex(t))

	out := decodeCallPath(t, tool, map[string]any{
		"from": "example.com/toolpath/app.(*Service).Run",
		"to":   "example.com/toolpath/app.DB.Save",
	})
	if !out.Found || len(out.Paths) != 1 {
		t.Fatalf("dispatch payload = %+v", out)
	}
	hop := out.Paths[0].Hops[0]
	if hop.EdgeKind != "interface_dispatch" || hop.ViaInterface != "example.com/toolpath/app.Store" {
		t.Fatalf("dispatch hop = %+v", hop)
	}

	out = decodeCallPath(t, tool, map[string]any{
		"from":                       "example.com/toolpath/app.Entry",
		"to":                         "example.com/toolpath/app.DB.Save",
		"include_interface_dispatch": false,
	})
	if out.Found || len(out.Paths) != 0 {
		t.Fatalf("dispatch-disabled payload = %+v", out)
	}
	if out.IncludeInterfaceDispatch {
		t.Fatalf("include_interface_dispatch = true")
	}
}

func TestFindCallPathToolSelfReverseAndLimits(t *testing.T) {
	tool := callPathTool(t, buildCallPathToolIndex(t))

	out := decodeCallPath(t, tool, map[string]any{
		"from": "example.com/toolpath/app.Top",
		"to":   "example.com/toolpath/app.Top",
	})
	if !out.Found || len(out.Paths) != 1 || out.Paths[0].Depth != 0 || len(out.Paths[0].Nodes) != 1 || len(out.Paths[0].Hops) != 0 {
		t.Fatalf("self payload = %+v", out)
	}

	out = decodeCallPath(t, tool, map[string]any{
		"from": "example.com/toolpath/app.Target",
		"to":   "example.com/toolpath/app.Top",
	})
	if out.Found || !out.ReversePathExists || !strings.Contains(out.ReversePathHint, "reverse path") {
		t.Fatalf("reverse hint payload = %+v", out)
	}

	out = decodeCallPath(t, tool, map[string]any{
		"from":      "example.com/toolpath/app.Top",
		"to":        "example.com/toolpath/app.Target",
		"max_depth": float64(1),
		"max_paths": float64(2),
	})
	if out.Found || out.MaxDepth != 1 || out.MaxPaths != 2 {
		t.Fatalf("limit payload = %+v", out)
	}
}

func TestFindCallPathToolErrors(t *testing.T) {
	tool := callPathTool(t, buildCallPathToolIndex(t))

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "missing from", args: map[string]any{"to": "example.com/toolpath/app.Target"}, want: "both 'from' and 'to' are required"},
		{name: "missing to", args: map[string]any{"from": "example.com/toolpath/app.Top"}, want: "both 'from' and 'to' are required"},
		{name: "missing symbol", args: map[string]any{"from": "missing", "to": "example.com/toolpath/app.Target"}, want: "symbol_not_found"},
		{name: "non-callable from", args: map[string]any{"from": "example.com/toolpath/app.Store", "to": "example.com/toolpath/app.Target"}, want: "not_callable_symbol"},
		{name: "non-callable to", args: map[string]any{"from": "example.com/toolpath/app.Top", "to": "example.com/toolpath/app.Store"}, want: "not_callable_symbol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, failed := tool.Run(t.Context(), tc.args)
			if !failed {
				t.Fatalf("expected failure, got %s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("error = %q, want %q", text, tc.want)
			}
		})
	}
}

package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func hotspotToolIndex() *index.Index {
	return &index.Index{
		RootDir: ".",
		Symbols: map[string]*index.Symbol{
			"m.A": {ID: "m.A", Name: "A", Kind: "func", Cyclomatic: 9, LOC: 30, ModuleArea: "a", File: "a.go", Line: 1},
			"m.B": {ID: "m.B", Name: "B", Kind: "method", RecvType: "T", Cyclomatic: 2, LOC: 10, ModuleArea: "b", File: "b.go", Line: 1, Untracked: true},
			"m.G": {ID: "m.G", Name: "G", Kind: "func", Cyclomatic: 20, LOC: 40, ModuleArea: "a", File: "gen.go", Line: 1, Generated: true},
			"m.T": {ID: "m.T", Name: "T", Kind: "struct", Fields: 4, LOC: 6, ModuleArea: "a", File: "a.go", Line: 1},
		},
		Callers:        map[string][]string{"m.A": {"x", "y"}},
		TestCovered:    map[string]bool{"m.A": true},
		UntrackedFiles: map[string]bool{"b.go": true},
	}
}

func hotspotsTool(t *testing.T) mcp.Tool {
	t.Helper()
	for _, tool := range New(staticIndexer{idx: hotspotToolIndex()}, render.NewRegistry(), nil) {
		if tool.Name == "get_hotspots" {
			return tool
		}
	}
	t.Fatal("get_hotspots not registered")
	return mcp.Tool{}
}

func hotspotItems(t *testing.T, args map[string]any) (map[string]any, []map[string]any) {
	t.Helper()
	text, failed := hotspotsTool(t).Run(context.Background(), args)
	if failed {
		t.Fatalf("get_hotspots failed: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	rawItems, _ := out["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		items = append(items, item)
	}
	return out, items
}

func TestHotspotsTool(t *testing.T) {
	out, items := hotspotItems(t, nil)
	if out["total"] != float64(2) || out["target"] != "func" || out["sort_by"] != "risk" {
		t.Fatalf("default response = %v", out)
	}
	if items[0]["name"] != "A" || items[0]["test_covered"] != true {
		t.Fatalf("top hotspot = %v", items[0])
	}
	if items[1]["name"] != "B" || items[1]["test_covered"] != false {
		t.Fatalf("second hotspot = %v", items[1])
	}
	for _, key := range []string{"canonical_id", "name", "file", "line", "module", "loc", "complexity", "fan_in", "risk_score", "test_covered"} {
		if _, ok := items[0][key]; !ok {
			t.Fatalf("item missing %s: %v", key, items[0])
		}
	}

	_, items = hotspotItems(t, map[string]any{"untested_only": true})
	if len(items) != 1 || items[0]["name"] != "B" {
		t.Fatalf("untested_only = %v", items)
	}

	_, items = hotspotItems(t, map[string]any{"include_generated": true})
	if len(items) != 3 || items[0]["name"] != "G" {
		t.Fatalf("include_generated = %v", items)
	}

	_, items = hotspotItems(t, map[string]any{"tracked_only": true})
	if len(items) != 1 || items[0]["name"] != "A" {
		t.Fatalf("tracked_only = %v", items)
	}

	_, items = hotspotItems(t, map[string]any{"module": "b"})
	if len(items) != 1 || items[0]["name"] != "B" {
		t.Fatalf("module filter = %v", items)
	}
	_, items = hotspotItems(t, map[string]any{"exclude_modules": []any{"a"}})
	if len(items) != 1 || items[0]["name"] != "B" {
		t.Fatalf("exclude_modules = %v", items)
	}

	out, items = hotspotItems(t, map[string]any{"target": "type", "sort_by": "complexity"})
	if out["target"] != "type" || len(items) != 1 || items[0]["fields"] != float64(4) {
		t.Fatalf("type target = %v", items)
	}
	_, items = hotspotItems(t, map[string]any{"target": "file"})
	if len(items) == 0 || items[0]["kind"] != "file" {
		t.Fatalf("file target = %v", items)
	}

	out, _ = hotspotItems(t, map[string]any{"limit": float64(500)})
	if out["limit"] != float64(100) {
		t.Fatalf("limit clamp = %v", out["limit"])
	}

	for _, bad := range []map[string]any{
		{"target": "bad"},
		{"sort_by": "bad"},
		{"target": "file", "untested_only": true},
	} {
		if _, failed := hotspotsTool(t).Run(context.Background(), bad); !failed {
			t.Fatalf("invalid args should fail: %v", bad)
		}
	}
}

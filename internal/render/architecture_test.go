package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func cycleIndex() *index.Index {
	return &index.Index{
		ModulePath: "example.com/cycles",
		Packages: map[string]*index.PkgInfo{
			"example.com/cycles/a": {RelPath: "a", ModuleArea: "a", Files: 2, GeneratedFiles: 1, UntrackedFiles: 1},
		},
		Symbols: map[string]*index.Symbol{
			"example.com/cycles.a.Gen":  {ID: "example.com/cycles.a.Gen", Name: "Gen", Kind: "func", Cyclomatic: 99, ModuleArea: "a", Generated: true},
			"example.com/cycles.a.Live": {ID: "example.com/cycles.a.Live", Name: "Live", Kind: "func", Cyclomatic: 7, ModuleArea: "a", Untracked: true, File: "a/scratch.go"},
		},
		Callers:        map[string][]string{"example.com/cycles.a.Live": {"x", "y", "z"}},
		UntrackedFiles: map[string]bool{"a/scratch.go": true},
		Modules: map[string]*index.ModSummary{
			"a": {Name: "a", Files: 2, GeneratedFiles: 1, UntrackedFiles: 1, OutModules: map[string][]string{"b": {"F"}}},
			"b": {Name: "b", Files: 1, OutModules: map[string][]string{"a": {"G"}}},
		},
		LargeFiles: []index.FileMetric{
			{Path: "a/generated.go", LOC: 900, Generated: true},
			{Path: "a/scratch.go", LOC: 500, Untracked: true},
		},
	}
}

func TestCodebaseMapCyclesUntrackedAndGenerated(t *testing.T) {
	idx := cycleIndex()
	registry := NewRegistry()

	jsonMap, ok := registry.RenderCodebaseMap("json", idx)
	if !ok {
		t.Fatal("json render failed")
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(jsonMap), &view); err != nil {
		t.Fatal(err)
	}
	if generated, _ := view["generated_files"].(float64); generated != 1 {
		t.Fatalf("generated_files = %v", view["generated_files"])
	}
	if untracked, _ := view["untracked_files"].(float64); untracked != 1 {
		t.Fatalf("untracked_files = %v", view["untracked_files"])
	}
	cycles, _ := view["module_cycles"].([]any)
	if len(cycles) != 1 || cycles[0] != "a -> b -> a" {
		t.Fatalf("module_cycles = %v", cycles)
	}
	if strings.Contains(jsonMap, "a.Gen") || strings.Contains(jsonMap, "generated.go") {
		t.Fatalf("generated content leaked into codebase map:\n%s", jsonMap)
	}
	if !strings.Contains(jsonMap, "a/scratch.go") {
		t.Fatalf("untracked hotspot missing:\n%s", jsonMap)
	}

	markdown, ok := registry.RenderCodebaseMap("markdown", idx)
	if !ok {
		t.Fatal("markdown render failed")
	}
	for _, want := range []string{"Cross-Module Cycles", "a -> b -> a", "[untracked]", "Top Hotspots"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "a.Gen") || strings.Contains(markdown, "generated.go") {
		t.Fatalf("generated content leaked into markdown:\n%s", markdown)
	}

	trackedJSON, ok := registry.RenderCodebaseMap("json", idx.TrackedView())
	if !ok {
		t.Fatal("tracked json render failed")
	}
	if strings.Contains(trackedJSON, "a/scratch.go") || strings.Contains(trackedJSON, "untracked\":true") {
		t.Fatalf("tracked_only did not exclude untracked data:\n%s", trackedJSON)
	}
}

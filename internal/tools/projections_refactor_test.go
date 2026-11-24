package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func TestCompactCodebaseModulePagination(t *testing.T) {
	tool := projectionTool(t, buildProjectionFixture(t), "get_codebase_map", nil)
	for _, test := range []struct {
		name           string
		topModules     float64
		wantReturned   int
		wantOmitted    int
		wantTotalCount int
	}{
		{name: "zero means all", topModules: 0, wantReturned: 2, wantTotalCount: 2},
		{name: "one module", topModules: 1, wantReturned: 1, wantOmitted: 1, wantTotalCount: 2},
		{name: "past end", topModules: 99, wantReturned: 2, wantTotalCount: 2},
		{name: "negative uses default", topModules: -1, wantReturned: 2, wantTotalCount: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			text, failed := tool.Run(context.Background(), map[string]any{
				"format": "json", "summary_only": true, "top_modules": test.topModules,
			})
			if failed {
				t.Fatal(text)
			}
			var out struct {
				Modules      []compactModule `json:"modules"`
				TotalModules int             `json:"total_modules"`
				Omitted      int             `json:"omitted_modules"`
				OmittedCount int             `json:"omitted_count"`
				TotalCount   int             `json:"total_count"`
				Returned     int             `json:"returned_count"`
				Truncated    bool            `json:"truncated"`
				Hint         string          `json:"hint"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Modules) != test.wantReturned || out.Returned != test.wantReturned || out.Omitted != test.wantOmitted || out.OmittedCount != test.wantOmitted || out.TotalModules != 2 || out.TotalCount != test.wantTotalCount {
				t.Fatalf("pagination = %+v", out)
			}
			if !out.Truncated || !strings.Contains(out.Hint, "summary_only=false") {
				t.Fatalf("summary metadata = %+v", out)
			}
			if test.wantOmitted > 0 && !strings.Contains(out.Hint, "currently 1; 0 = all 2 modules") {
				t.Fatalf("omission hint = %q", out.Hint)
			}
		})
	}
}

func TestCompactProjectionValidationErrors(t *testing.T) {
	idx := buildProjectionFixture(t)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{tool: "get_codebase_map", args: map[string]any{"detail": "bad"}, want: "invalid detail: bad"},
		{tool: "get_codebase_map", args: map[string]any{"sort_by": "bad"}, want: "invalid sort_by: bad"},
		{tool: "get_codebase_map", args: map[string]any{"format": "not-a-format"}, want: "unsupported format: not-a-format"},
		{tool: "get_module_overview", args: map[string]any{"module": "alpha", "detail": "bad"}, want: "invalid detail: bad"},
		{tool: "get_module_overview", args: map[string]any{"module": "alpha", "format": "not-a-format"}, want: "unsupported format: not-a-format"},
		{tool: "get_module_overview", args: map[string]any{"module": "missing"}, want: `module "missing" not found; available: alpha, beta`},
	}
	for _, test := range cases {
		t.Run(test.tool+"/"+test.want, func(t *testing.T) {
			tool := projectionTool(t, idx, test.tool, nil)
			text, failed := tool.Run(context.Background(), test.args)
			if !failed || text != test.want {
				t.Fatalf("error = %q failed=%v, want %q", text, failed, test.want)
			}
		})
	}
}

func TestCompactProjectionsAreDeterministic(t *testing.T) {
	idx := buildProjectionFixture(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{tool: "get_codebase_map", args: map[string]any{"format": "json", "top_modules": float64(0)}},
		{tool: "get_codebase_map", args: map[string]any{"format": "markdown", "summary_only": true, "top_modules": float64(1)}},
		{tool: "get_module_overview", args: map[string]any{"module": "alpha", "format": "json"}},
		{tool: "get_module_overview", args: map[string]any{"module": "alpha", "format": "markdown", "include_generated": true}},
	}
	for _, test := range cases {
		t.Run(test.tool+"/"+test.args["format"].(string), func(t *testing.T) {
			tool := projectionTool(t, idx, test.tool, nil)
			want, failed := tool.Run(context.Background(), test.args)
			if failed {
				t.Fatal(want)
			}
			for i := 0; i < 25; i++ {
				got, failed := tool.Run(context.Background(), test.args)
				if failed {
					t.Fatal(got)
				}
				if got != want {
					t.Fatalf("projection changed on run %d\nwant: %s\ngot: %s", i, want, got)
				}
			}
		})
	}
}

func TestProjectionPaginationHelpers(t *testing.T) {
	modules := []compactModule{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	for _, test := range []struct {
		name        string
		top         int
		wantNames   []string
		wantOmitted int
	}{
		{name: "zero means all", top: 0, wantNames: []string{"a", "b", "c"}},
		{name: "bounded", top: 1, wantNames: []string{"a"}, wantOmitted: 2},
		{name: "past end", top: 99, wantNames: []string{"a", "b", "c"}},
		{name: "negative means all", top: -1, wantNames: []string{"a", "b", "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, omitted := paginateCompactModules(modules, test.top)
			names := make([]string, 0, len(page))
			for _, mod := range page {
				names = append(names, mod.Name)
			}
			if !reflect.DeepEqual(names, test.wantNames) || omitted != test.wantOmitted {
				t.Fatalf("page = %v omitted=%d", names, omitted)
			}
		})
	}
	var nilModules []compactModule
	if page, omitted := paginateCompactModules(nilModules, 1); page != nil || omitted != 0 {
		t.Fatalf("nil module page = %#v omitted=%d", page, omitted)
	}

	files := make([]index.FileMetric, 12)
	for i := range files {
		files[i] = index.FileMetric{Path: fmt.Sprintf("%02d.go", i), LOC: i + 1}
	}
	files[5].Generated = true
	page, total := pageLargeFiles(files, false)
	if total != 11 || len(page) != 10 {
		t.Fatalf("non-generated page len=%d total=%d", len(page), total)
	}
	for _, file := range page {
		if file.Generated {
			t.Fatalf("generated file leaked into page: %+v", file)
		}
	}
	withGenerated, total := pageLargeFiles(files, true)
	if total != 12 || len(withGenerated) != 10 || !withGenerated[5].Generated {
		t.Fatalf("generated page len=%d total=%d files=%+v", len(withGenerated), total, withGenerated)
	}
	empty, total := pageLargeFiles(nil, false)
	if empty == nil || len(empty) != 0 || total != 0 {
		t.Fatalf("empty file page = %#v total=%d", empty, total)
	}
	if got := limitFiles(files, 0); len(got) != len(files) {
		t.Fatalf("zero file limit returned %d files", len(got))
	}
	if got := limitFiles(files, -1); len(got) != len(files) {
		t.Fatalf("negative file limit returned %d files", len(got))
	}
}

func TestProjectionOrderingHelpers(t *testing.T) {
	idx := &index.Index{Modules: map[string]*index.ModSummary{
		"alpha": {Name: "alpha", ProdLOC: 10, InModules: map[string][]string{"x": nil, "y": nil}, OutModules: map[string][]string{"z": nil}},
		"beta":  {Name: "beta", ProdLOC: 10, InModules: map[string][]string{"x": nil}, OutModules: map[string][]string{"y": nil, "z": nil}},
		"gamma": {Name: "gamma", ProdLOC: 5, InModules: map[string][]string{"x": nil, "y": nil}, OutModules: map[string][]string{"x": nil, "y": nil}},
	}}
	for _, test := range []struct {
		sortBy string
		want   []string
	}{
		{sortBy: "prod_loc", want: []string{"alpha", "beta", "gamma"}},
		{sortBy: "fan_in", want: []string{"alpha", "gamma", "beta"}},
		{sortBy: "fan_out", want: []string{"beta", "gamma", "alpha"}},
	} {
		mods := compactModules(idx, test.sortBy)
		got := make([]string, 0, len(mods))
		for _, mod := range mods {
			got = append(got, mod.Name)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("compactModules(%q) = %v, want %v", test.sortBy, got, test.want)
		}
	}

	edges := compactEdges(idx)
	wantEdges := []compactEdge{
		{From: "alpha", To: "z", Symbols: 0},
		{From: "beta", To: "y", Symbols: 0},
		{From: "beta", To: "z", Symbols: 0},
		{From: "gamma", To: "x", Symbols: 0},
		{From: "gamma", To: "y", Symbols: 0},
	}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Fatalf("edges = %+v, want %+v", edges, wantEdges)
	}
	moduleEdges := compactModuleEdges(idx.Modules["gamma"])
	if !reflect.DeepEqual(moduleEdges.Out, []moduleEdgeCount{{Module: "x"}, {Module: "y"}}) {
		t.Fatalf("outgoing edges = %+v", moduleEdges.Out)
	}

	packageIdx := &index.Index{Packages: map[string]*index.PkgInfo{
		"z": {PkgPath: "example.com/z", RelPath: "z", ModuleArea: "alpha"},
		"a": {PkgPath: "example.com/a", RelPath: "a", ModuleArea: "alpha"},
		"b": {PkgPath: "example.com/b", RelPath: "b", ModuleArea: "beta"},
	}}
	packages := compactPackageRollups(packageIdx, "alpha")
	if len(packages) != 2 || packages[0].RelPath != "a" || packages[1].RelPath != "z" {
		t.Fatalf("package rollups = %+v", packages)
	}
	if empty := compactPackageRollups(packageIdx, "missing"); empty == nil || len(empty) != 0 {
		t.Fatalf("empty package rollups = %#v", empty)
	}
}

func TestProjectionMetadataHelpers(t *testing.T) {
	out := compactCodebase{
		LargeFiles:      make([]index.FileMetric, 2),
		TotalLargeFiles: 4,
		TopHotspots:     make([]index.Hotspot, 1),
		TotalHotspots:   3,
	}
	finalizeCompactCodebase(&out, false, 15, 0)
	wantHint := `increase the large-file window or use detail="full" for all 4 large files; call get_hotspots for all 3 hotspots`
	if !out.Truncated || out.Hint != wantHint {
		t.Fatalf("codebase metadata = %+v", out)
	}

	out = compactCodebase{}
	finalizeCompactCodebase(&out, true, 1, 2)
	wantHint = `set summary_only=false to include dependency edges, cycles, large files, and hotspots; increase top_modules (currently 1; 0 = all 3 modules) or use detail="full"`
	if !out.Truncated || out.Hint != wantHint {
		t.Fatalf("summary metadata = %+v", out)
	}

	mod := &index.ModSummary{Name: "alpha", GeneratedFiles: 1, GeneratedSymbols: 4, GeneratedLOC: 40}
	overview := compactModuleOverview{
		GeneratedOmitted: &generatedOmitted{Files: 1, Symbols: 4, LOC: 40},
		LargeFiles:       make([]index.FileMetric, 1),
		TotalLargeFiles:  3,
		TopHotspots:      make([]index.Hotspot, 2),
		TotalHotspots:    5,
	}
	wantHint = `set include_generated=true to include 4 generated symbols and 40 generated LOC; large files are capped at 10 of 3; call get_hotspots with module="alpha" for all 5 hotspots`
	if got := moduleOverviewHint(overview, mod); got != wantHint {
		t.Fatalf("module hint = %q, want %q", got, wantHint)
	}
	finalizeCompactModuleOverview(&overview, mod)
	if !overview.Truncated || overview.Hint != wantHint {
		t.Fatalf("module metadata = %+v", overview)
	}

	overview = compactModuleOverview{IncludeGenerated: true}
	finalizeCompactModuleOverview(&overview, mod)
	if overview.GeneratedOmitted != nil || overview.Truncated || overview.Hint != "" {
		t.Fatalf("inclusive module metadata = %+v", overview)
	}
}

func TestModuleResponseFilePagination(t *testing.T) {
	idx := buildProjectionFixture(t)
	mod := *idx.Modules["alpha"]
	mod.LargeFiles = make([]index.FileMetric, 12)
	for i := range mod.LargeFiles {
		mod.LargeFiles[i] = index.FileMetric{Path: fmt.Sprintf("%02d.go", i), LOC: i + 1}
	}
	mod.LargeFiles[5].Generated = true
	out := compactModuleResponse(idx, &mod, false, index.StateReady)
	if out.TotalLargeFiles != 11 || len(out.LargeFiles) != 10 || out.GeneratedOmitted != nil {
		t.Fatalf("module response = %+v", out)
	}
	finalizeCompactModuleOverview(&out, &mod)
	if out.GeneratedOmitted == nil || !out.Truncated || !strings.Contains(out.Hint, "large files are capped at 10 of 11") {
		t.Fatalf("finalized module response = %+v", out)
	}
	out = compactModuleResponse(idx, &mod, true, index.StateReady)
	if out.TotalLargeFiles != 12 || len(out.LargeFiles) != 10 || !out.LargeFiles[5].Generated {
		t.Fatalf("inclusive module response = %+v", out)
	}
}

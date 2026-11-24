package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

type apiSurfaceTestPayload struct {
	Package          string                      `json:"package"`
	TotalCount       int                         `json:"total_count"`
	Offset           int                         `json:"offset"`
	Limit            int                         `json:"limit"`
	Returned         int                         `json:"returned"`
	Truncated        bool                        `json:"truncated"`
	NextOffset       *int                        `json:"next_offset"`
	Hint             string                      `json:"hint"`
	GeneratedOmitted *generatedOmitted           `json:"generated_omitted"`
	Symbols          map[string][]apiSurfaceItem `json:"symbols"`
}

func TestAPISurfacePaginationEdges(t *testing.T) {
	idx := buildProjectionFixture(t)
	tool := projectionTool(t, idx, "get_api_surface", nil)
	next := 2
	nextFromStart := 1
	cases := []struct {
		name         string
		args         map[string]any
		wantTotal    int
		wantOffset   int
		wantLimit    int
		wantReturned int
		wantNext     *int
		wantFirst    string
	}{
		{
			name:      "middle page",
			args:      map[string]any{"package": "modules/alpha", "include_generated": true, "offset": float64(1), "limit": float64(1)},
			wantTotal: 7, wantOffset: 1, wantLimit: 1, wantReturned: 1, wantNext: &next, wantFirst: "AlsoNormal",
		},
		{
			name:      "offset past end clamps to total",
			args:      map[string]any{"package": "modules/alpha", "include_generated": true, "offset": float64(99), "limit": float64(2)},
			wantTotal: 7, wantOffset: 7, wantLimit: 2,
		},
		{
			name:      "zero limit is unlimited",
			args:      map[string]any{"package": "modules/alpha", "include_generated": true, "offset": float64(1), "limit": float64(0)},
			wantTotal: 7, wantOffset: 1, wantLimit: 0, wantReturned: 6, wantFirst: "AlsoNormal",
		},
		{
			name:      "negative limit is also unlimited",
			args:      map[string]any{"package": "modules/alpha", "include_generated": true, "offset": float64(1), "limit": float64(-3)},
			wantTotal: 7, wantOffset: 1, wantLimit: 0, wantReturned: 6, wantFirst: "AlsoNormal",
		},
		{
			name:      "negative offset falls back to zero",
			args:      map[string]any{"package": "modules/alpha", "include_generated": true, "offset": float64(-1), "limit": float64(1)},
			wantTotal: 7, wantOffset: 0, wantLimit: 1, wantReturned: 1, wantNext: &nextFromStart, wantFirst: "API",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			text, failed := tool.Run(context.Background(), test.args)
			if failed {
				t.Fatal(text)
			}
			var out apiSurfaceTestPayload
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatal(err)
			}
			if out.TotalCount != test.wantTotal || out.Offset != test.wantOffset || out.Limit != test.wantLimit || out.Returned != test.wantReturned {
				t.Fatalf("pagination = %+v", out)
			}
			if !reflect.DeepEqual(out.NextOffset, test.wantNext) {
				t.Fatalf("next_offset = %v, want %v", out.NextOffset, test.wantNext)
			}
			wantTruncated := test.wantNext != nil
			if out.Truncated != wantTruncated {
				t.Fatalf("truncated = %v, want %v", out.Truncated, wantTruncated)
			}
			if out.Symbols == nil {
				t.Fatal("symbols must be an object, not null")
			}
			if test.wantFirst == "" {
				if len(out.Symbols) != 0 {
					t.Fatalf("past-end symbols = %+v, want empty object", out.Symbols)
				}
				return
			}
			if got := out.Symbols["func"]; len(got) == 0 || got[0].Name != test.wantFirst || (test.wantReturned == 1 && len(got) != 1) {
				first := out.Symbols["interface"]
				if test.wantFirst == "API" && len(first) == 1 && first[0].Name == "API" {
					return
				}
				t.Fatalf("func page = %+v, want first %q", got, test.wantFirst)
			}
		})
	}
}

func TestAPISurfaceKindFilteringAndRanking(t *testing.T) {
	idx := buildProjectionFixture(t)
	tool := projectionTool(t, idx, "get_api_surface", nil)
	text, failed := tool.Run(context.Background(), map[string]any{
		"package": "modules/alpha", "include_generated": true, "kind": "func", "limit": float64(0),
	})
	if failed {
		t.Fatal(text)
	}
	var out apiSurfaceTestPayload
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"AlsoNormal", "GeneratedAPI", "GeneratedHotspot", "Normal", "ThirdNormal"}
	if out.TotalCount != len(want) || out.Returned != len(want) || len(out.Symbols) != 1 {
		t.Fatalf("filtered payload = %+v", out)
	}
	got := make([]string, 0, len(out.Symbols["func"]))
	for _, item := range out.Symbols["func"] {
		got = append(got, item.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("func order = %v, want %v", got, want)
	}

	pkg := "example.com/proj/modules/alpha"
	symbols := []*index.Symbol{
		{ID: "var", Name: "ZVar", Kind: "var", PkgPath: pkg, Exported: true},
		{ID: "generated-var", Name: "GeneratedVar", Kind: "var", PkgPath: pkg, Exported: true, Generated: true},
		{ID: "method", Name: "Zulu", Kind: "method", RecvType: "Thing", PkgPath: pkg, Exported: true},
		{ID: "same-b", Name: "Same", Kind: "func", PkgPath: pkg, Exported: true},
		{ID: "same-a", Name: "Same", Kind: "func", PkgPath: pkg, Exported: true},
		{ID: "struct", Name: "AType", Kind: "struct", PkgPath: pkg, Exported: true},
		{ID: "type", Name: "ZType", Kind: "type", PkgPath: pkg, Exported: true},
		{ID: "interface", Name: "API", Kind: "interface", PkgPath: pkg, Exported: true},
		{ID: "const", Name: "Max", Kind: "const", PkgPath: pkg, Exported: true},
		{ID: "field", Name: "Mystery", Kind: "field", PkgPath: pkg, Exported: true},
		{ID: "private", Name: "Hidden", Kind: "func", PkgPath: pkg},
		{ID: "test", Name: "TestOnly", Kind: "func", PkgPath: pkg, Exported: true, IsTest: true},
		{ID: "other", Name: "Other", Kind: "func", PkgPath: "example.com/other", Exported: true},
	}
	manual := &index.Index{Symbols: make(map[string]*index.Symbol, len(symbols))}
	for _, sym := range symbols {
		manual.Symbols[sym.ID] = sym
	}
	items, omitted := (&toolset{}).collectAPISurface(context.Background(), manual, pkg, false, "")
	got = got[:0]
	for _, item := range items {
		got = append(got, item.Name+":"+item.ID)
	}
	want = []string{"API:interface", "AType:struct", "ZType:type", "(Thing).Zulu:method", "Same:same-a", "Same:same-b", "Max:const", "ZVar:var", "Mystery:field"}
	if !reflect.DeepEqual(got, want) || omitted != 1 {
		t.Fatalf("ranked items = %v omitted=%d, want %v omitted=1", got, omitted, want)
	}

	functions, omitted := (&toolset{}).collectAPISurface(context.Background(), manual, pkg, false, "func")
	if len(functions) != 3 || omitted != 0 {
		t.Fatalf("func filter = %+v omitted=%d", functions, omitted)
	}
	types, omitted := (&toolset{}).collectAPISurface(context.Background(), manual, pkg, false, "type")
	if len(types) != 2 || omitted != 0 {
		t.Fatalf("type filter = %+v omitted=%d", types, omitted)
	}
}

func TestAPIKindHelpers(t *testing.T) {
	matches := []struct {
		kind, filter string
		want         bool
	}{
		{"method", "", true}, {"method", "func", true}, {"struct", "type", true},
		{"func", "func", true}, {"func", "method", false}, {"interface", "type", false},
	}
	for _, test := range matches {
		if got := apiKindMatches(test.kind, test.filter); got != test.want {
			t.Errorf("apiKindMatches(%q, %q) = %v, want %v", test.kind, test.filter, got, test.want)
		}
	}
	ranks := map[string]int{"interface": 0, "type": 1, "struct": 1, "func": 2, "method": 2, "const": 3, "var": 4, "field": 5}
	for kind, want := range ranks {
		if got := apiKindRank(kind); got != want {
			t.Errorf("apiKindRank(%q) = %d, want %d", kind, got, want)
		}
	}
}

func TestReferencesLimitAndNilSliceRegression(t *testing.T) {
	idx := buildProjectionFixture(t)
	tool := projectionTool(t, idx, "get_references", nil)
	targetID := ""
	for _, sym := range idx.Symbols {
		if sym.Name == "ThirdNormal" {
			targetID = sym.ID
		}
	}
	if targetID == "" {
		t.Fatal("ThirdNormal not found")
	}
	idx.XRefs[targetID] = []index.XRef{{TargetID: targetID, File: "a.go", Line: 1}, {TargetID: targetID, File: "b.go", Line: 2}, {TargetID: targetID, File: "c.go", Line: 3}}
	for _, test := range []struct {
		name      string
		limit     any
		want      int
		truncated bool
	}{
		{name: "omitted uses default", want: 3},
		{name: "zero uses default", limit: float64(0), want: 3},
		{name: "negative uses default", limit: float64(-1), want: 3},
		{name: "positive truncates", limit: float64(1), want: 1, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"symbol_id": targetID}
			if test.limit != nil {
				args["limit"] = test.limit
			}
			text, failed := tool.Run(context.Background(), args)
			if failed {
				t.Fatal(text)
			}
			var out struct {
				Total     int          `json:"total"`
				Truncated bool         `json:"truncated"`
				Refs      []index.XRef `json:"refs"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatal(err)
			}
			if out.Total != 3 || len(out.Refs) != test.want || out.Truncated != test.truncated {
				t.Fatalf("references = %+v", out)
			}
		})
	}

	emptyID := canonicalName(t, idx, "AlsoNormal")
	text, failed := tool.Run(context.Background(), map[string]any{"symbol_id": emptyID})
	if failed {
		t.Fatal(text)
	}
	var empty struct {
		Refs []index.XRef `json:"refs"`
	}
	if err := json.Unmarshal([]byte(text), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Refs == nil || len(empty.Refs) != 0 {
		t.Fatalf("empty refs = %#v, want []", empty.Refs)
	}

	impl := projectionTool(t, idx, "find_implementations", nil)
	text, failed = impl.Run(context.Background(), map[string]any{"symbol_id": canonicalName(t, idx, "API")})
	if failed {
		t.Fatal(text)
	}
	var implementations struct {
		Items []index.ImplMatch `json:"implementations"`
	}
	if err := json.Unmarshal([]byte(text), &implementations); err != nil {
		t.Fatal(err)
	}
	if implementations.Items == nil || len(implementations.Items) != 0 {
		t.Fatalf("empty implementations = %#v, want []", implementations.Items)
	}
}

func TestSymbolToolInvalidArgumentPayloads(t *testing.T) {
	idx := buildProjectionFixture(t)
	for _, name := range []string{"get_references", "get_call_graph", "find_implementations", "get_function_body"} {
		t.Run(name, func(t *testing.T) {
			tool := projectionTool(t, idx, name, nil)
			text, failed := tool.Run(context.Background(), map[string]any{"symbol_id": "missing.Symbol"})
			if !failed {
				t.Fatalf("missing symbol did not fail: %s", text)
			}
			var out struct {
				Error string `json:"error"`
				Input string `json:"input"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("invalid error payload %q: %v", text, err)
			}
			if out.Error != "symbol_not_found" || out.Input != "missing.Symbol" {
				t.Fatalf("error payload = %+v", out)
			}
		})
	}

	api := projectionTool(t, idx, "get_api_surface", nil)
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{"package": "missing"}, want: "package not found: missing"},
		{args: map[string]any{"package": "modules/alpha", "kind": "struct"}, want: "invalid kind: struct"},
	} {
		text, failed := api.Run(context.Background(), test.args)
		if !failed || text != test.want {
			t.Fatalf("api error = %q failed=%v, want %q", text, failed, test.want)
		}
	}
}

func TestDeadCodeLimitDefaults(t *testing.T) {
	idx := buildToolFixture(t)
	tool := projectionTool(t, idx, "get_dead_code", nil)
	for _, test := range []struct {
		name  string
		limit any
		want  int
	}{
		{name: "omitted uses default", want: 3},
		{name: "zero uses default", limit: float64(0), want: 3},
		{name: "negative uses default", limit: float64(-1), want: 3},
		{name: "positive truncates", limit: float64(1), want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{}
			if test.limit != nil {
				args["limit"] = test.limit
			}
			text, failed := tool.Run(context.Background(), args)
			if failed {
				t.Fatal(text)
			}
			var out struct {
				Total      int               `json:"total_candidates"`
				Candidates []json.RawMessage `json:"candidates"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatal(err)
			}
			if out.Total != 3 || len(out.Candidates) != test.want {
				t.Fatalf("dead-code payload = %+v", out)
			}
		})
	}
}

func TestAPISurfacePaginationAndHintHelpers(t *testing.T) {
	items := []apiSurfaceItem{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	for _, test := range []struct {
		name          string
		offset, limit int
		wantOffset    int
		wantIDs       []string
		wantNext      int
	}{
		{name: "bounded page", offset: 1, limit: 2, wantOffset: 1, wantIDs: []string{"b", "c"}, wantNext: 3},
		{name: "past end", offset: 99, limit: 2, wantOffset: 4, wantIDs: []string{}},
		{name: "zero unlimited", offset: 1, limit: 0, wantOffset: 1, wantIDs: []string{"b", "c", "d"}},
		{name: "negative unlimited", offset: 1, limit: -1, wantOffset: 1, wantIDs: []string{"b", "c", "d"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := paginateAPISurface(items, test.offset, test.limit)
			ids := make([]string, 0, len(page.Items))
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			if page.Offset != test.wantOffset || page.Limit != test.limit || !reflect.DeepEqual(ids, test.wantIDs) || page.NextOffset != test.wantNext || page.HasNext != (test.wantNext != 0) {
				t.Fatalf("page = %+v ids=%v", page, ids)
			}
		})
	}
	var nilItems []apiSurfaceItem
	if page := paginateAPISurface(nilItems, 10, 0); page.Items != nil || page.Offset != 0 {
		t.Fatalf("nil page = %+v", page)
	}

	info := &index.PkgInfo{GeneratedFiles: 1, GeneratedLOC: 30}
	page := paginateAPISurface(items, 1, 1)
	out := map[string]any{}
	addAPISurfaceHints(out, "example.com/p", false, page, info, 3)
	wantHint := `call get_api_surface with package="example.com/p", offset=2, limit=1 for the next page; limit=0 returns all symbols; call get_api_surface with package="example.com/p", include_generated=true to include 3 generated symbol(s)`
	if out["next_offset"] != 2 || out["hint"] != wantHint || !reflect.DeepEqual(out["generated_omitted"], map[string]any{"files": 1, "symbols": 3, "loc": 30}) {
		t.Fatalf("hints = %#v", out)
	}

	out = map[string]any{}
	addAPISurfaceHints(out, "example.com/p", true, page, info, 0)
	if _, ok := out["generated_omitted"]; ok {
		t.Fatalf("generated metadata must be absent: %#v", out)
	}
	if _, ok := out["hint"]; !ok || !strings.Contains(out["hint"].(string), "offset=2") {
		t.Fatalf("next-page hint missing: %#v", out)
	}

	out = map[string]any{}
	addAPISurfaceHints(out, "example.com/p", false, paginateAPISurface(items, 4, 1), &index.PkgInfo{}, 0)
	if len(out) != 0 {
		t.Fatalf("empty page must not add hints: %#v", out)
	}
}

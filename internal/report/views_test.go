package report

import (
	"reflect"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func TestModuleViewsOrdering(t *testing.T) {
	idx := &index.Index{
		Modules: map[string]*index.ModSummary{
			"z": {Name: "z", ProdLOC: 20},
			"a": {Name: "a", ProdLOC: 10, OutModules: map[string][]string{"z": {"one", "two"}, "a": nil}},
		},
		Summary: &index.SummaryData{ByID: map[string]*index.SummarySymbol{
			"mod:a": {ID: "mod:a", Eligible: true},
		}},
	}
	cached := map[string]cache.Cached{"mod:a": {State: cache.StateSynced}}
	graph := buildModuleGraph(idx, cached)
	if len(graph.Nodes) != 2 || graph.Nodes[0].ID != "mod:a" || graph.Nodes[1].ID != "mod:z" {
		t.Fatalf("module graph nodes = %+v", graph.Nodes)
	}
	wantEdges := []render.HTMLGraphEdge{
		{From: "mod:a", To: "mod:a", Weight: 0},
		{From: "mod:a", To: "mod:z", Weight: 2},
	}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("module graph edges = %+v, want %+v", graph.Edges, wantEdges)
	}
	if graph.Nodes[0].SyncState != "synced" || !graph.Nodes[0].Eligible {
		t.Fatalf("module graph metadata = %+v", graph.Nodes[0])
	}
	cards := buildModuleCards(idx, cached)
	if len(cards) != 2 || cards[0].ID != "mod:z" || cards[1].ID != "mod:a" {
		t.Fatalf("module cards = %+v", cards)
	}
}

func TestPackageViewsOrdering(t *testing.T) {
	idx := &index.Index{
		Packages: map[string]*index.PkgInfo{
			"example.com/z": {PkgPath: "example.com/z", RelPath: "z"},
			"example.com/a": {PkgPath: "example.com/a", RelPath: "a", Imports: []string{"example.com/z"}},
		},
		RevImports: map[string][]string{"example.com/z": {"example.com/a"}},
		Summary:    &index.SummaryData{},
	}
	graph := buildPackageGraph(idx, nil)
	if len(graph.Nodes) != 2 || graph.Nodes[0].ID != "pkg:example.com/a" || graph.Nodes[1].ID != "pkg:example.com/z" {
		t.Fatalf("package graph nodes = %+v", graph.Nodes)
	}
	wantEdges := []render.HTMLGraphEdge{{From: "pkg:example.com/a", To: "pkg:example.com/z", Weight: 1}}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("package graph edges = %+v, want %+v", graph.Edges, wantEdges)
	}
	cards := buildPackageCards(idx, nil)
	if len(cards) != 2 || cards[0].RelPath != "a" || cards[1].RelPath != "z" {
		t.Fatalf("package cards = %+v", cards)
	}
	if cards[0].FanOut != 1 || cards[1].FanIn != 1 {
		t.Fatalf("package card coupling = %+v", cards)
	}
}

func TestBuildInterfacesOrderingAndInvalidMatches(t *testing.T) {
	idx := &index.Index{
		Symbols: map[string]*index.Symbol{
			"iface-b": {ID: "iface-b", Name: "IfaceB", Kind: "interface", PkgPath: "example.com/z"},
			"iface-a": {ID: "iface-a", Name: "IfaceA", Kind: "interface", PkgPath: "example.com/a"},
			"impl-z":  {ID: "impl-z", Name: "A", Kind: "struct", PkgPath: "example.com/a"},
			"impl-a":  {ID: "impl-a", Name: "Z", Kind: "struct", PkgPath: "example.com/a"},
			"impl-b":  {ID: "impl-b", Name: "A", Kind: "struct", PkgPath: "example.com/z"},
			"invalid": {ID: "invalid", Name: "Ignored", Kind: "var", PkgPath: "example.com/a"},
		},
		IfaceImpls: map[string][]index.ImplMatch{
			"iface-a": {
				{TypeID: "impl-a"},
				{TypeID: "missing"},
				{TypeID: "impl-z", ViaPtr: true},
				{TypeID: "invalid"},
			},
			"iface-b": {{TypeID: "impl-b"}},
		},
	}
	_, canonicalByExisting := canonicalSymbols(idx)
	got := buildInterfaces(idx, canonicalByExisting)
	want := []render.HTMLInterface{
		{
			ID: "sym:example.com/a.IfaceA", Name: "IfaceA", PkgPath: "example.com/a",
			Implementations: []render.HTMLImpl{
				{ID: "sym:example.com/a.A", Name: "A", PkgPath: "example.com/a", ViaPtr: true},
				{ID: "sym:example.com/a.Z", Name: "Z", PkgPath: "example.com/a"},
			},
		},
		{
			ID: "sym:example.com/z.IfaceB", Name: "IfaceB", PkgPath: "example.com/z",
			Implementations: []render.HTMLImpl{{ID: "sym:example.com/z.A", Name: "A", PkgPath: "example.com/z"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildInterfaces = %+v, want %+v", got, want)
	}
}

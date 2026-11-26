package index

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestModuleCyclesFindsAllElementaryCycles(t *testing.T) {
	module := func(out ...string) *ModSummary {
		targets := map[string][]string{}
		for _, dst := range out {
			targets[dst] = nil
		}
		return &ModSummary{OutModules: targets, InModules: map[string][]string{}}
	}
	seriesDiamond := &Index{Modules: map[string]*ModSummary{
		"a": module("b", "c"),
		"b": module("d"),
		"c": module("d"),
		"d": module("e", "f"),
		"e": module("a"),
		"f": module("a"),
	}}
	cases := []struct {
		name    string
		idx     *Index
		want    [][]string
		notMore bool
	}{
		{
			name: "series diamond",
			idx:  seriesDiamond,
			want: [][]string{
				{"a", "b", "d", "e", "a"},
				{"a", "b", "d", "f", "a"},
				{"a", "c", "d", "e", "a"},
				{"a", "c", "d", "f", "a"},
			},
		},
		{
			name: "acyclic graph",
			idx: &Index{Modules: map[string]*ModSummary{
				"a": module("b"),
				"b": module("c"),
				"c": module(),
			}},
			want: nil,
		},
		{
			name: "overlapping cycles are not pruned",
			idx: &Index{Modules: map[string]*ModSummary{
				"a": module("b", "c"),
				"b": module("c", "a"),
				"c": module("a"),
			}},
			want: [][]string{
				{"a", "b", "a"},
				{"a", "b", "c", "a"},
				{"a", "c", "a"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.idx.ModuleCycles()
			if len(got) != len(tc.want) {
				t.Fatalf("cycles = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if strings.Join(got[i], ">") != strings.Join(tc.want[i], ">") {
					t.Fatalf("cycle[%d] = %v, want %v (all: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestImpactBySymbolNonFuncTransitiveCallers(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/impact", map[string]string{
		"models/models.go": `package models

type Order struct{ ID int }

var Sentinel = 1

func UseOrder(o Order) int { return o.ID }
func WrapOrder() int      { return UseOrder(Order{}) }
func TopOrder() int       { return WrapOrder() + 1 }

func UseSentinel() int     { return Sentinel }
func MiddleSentinel() int  { return UseSentinel() }
func TopSentinel() int     { return MiddleSentinel() }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "example.com/impact/models."
	cases := []struct {
		name         string
		id           string
		depth        int
		want         []string
		notAtShallow []string
	}{
		{
			name:         "struct depth 1",
			id:           prefix + "Order",
			depth:        1,
			want:         []string{prefix + "UseOrder", prefix + "WrapOrder"},
			notAtShallow: []string{prefix + "TopOrder"},
		},
		{
			name:  "struct depth 2",
			id:    prefix + "Order",
			depth: 2,
			want:  []string{prefix + "UseOrder", prefix + "WrapOrder", prefix + "TopOrder"},
		},
		{
			name:  "field depth 3",
			id:    prefix + "Order.ID",
			depth: 3,
			want:  []string{prefix + "UseOrder", prefix + "WrapOrder", prefix + "TopOrder"},
		},
		{
			name:  "var depth 3",
			id:    prefix + "Sentinel",
			depth: 3,
			want:  []string{prefix + "UseSentinel", prefix + "MiddleSentinel", prefix + "TopSentinel"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sym := idx.Symbols[tc.id]
			if sym == nil {
				t.Fatalf("missing symbol %s", tc.id)
			}
			impact := idx.ImpactBySymbol(sym, tc.depth)
			for _, want := range tc.want {
				if !slices.Contains(impact.TransitiveCallers, want) {
					t.Fatalf("transitive callers missing %s: %+v", want, impact.TransitiveCallers)
				}
			}
			if tc.notAtShallow != nil {
				shallow := idx.ImpactBySymbol(sym, 1)
				for _, absent := range tc.notAtShallow {
					if slices.Contains(shallow.TransitiveCallers, absent) {
						t.Fatalf("depth 1 unexpectedly reached %s: %+v", absent, shallow.TransitiveCallers)
					}
				}
			}
		})
	}
}

func TestNearestSymbolSuggestions(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/suggest", map[string]string{
		"models/models.go": `package models

type OrderService struct{}

func (OrderService) Process() {}

func HandleRequest() {}

var DefaultConfig = 1
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query string
		want  string
	}{
		{query: "OrdreService.Prxcess", want: "Process"},
		{query: "HandleRequst", want: "HandleRequest"},
		{query: "DefualtConfg", want: "DefaultConfig"},
		{query: "models.OrdreService", want: "OrderService"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			_, err := idx.SingleSymbol(tc.query)
			var notFound *SymbolNotFoundError
			if err == nil || !strings.Contains(err.Error(), "symbol not found") {
				t.Fatalf("error = %v", err)
			}
			if !errors.As(err, &notFound) {
				t.Fatalf("error type = %T", err)
			}
			if len(notFound.Suggestions) == 0 {
				t.Fatalf("no suggestions for %q", tc.query)
			}
			if !slices.ContainsFunc(notFound.Suggestions, func(s string) bool { return strings.Contains(s, tc.want) }) {
				t.Fatalf("suggestions %v do not include %s", notFound.Suggestions, tc.want)
			}
			if !strings.Contains(err.Error(), "nearest matches:") {
				t.Fatalf("error message lost nearest matches: %v", err)
			}
		})
	}
}

func TestHotspotsExcludeTestSymbols(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/hot", map[string]string{
		"pkg/pkg.go": `package pkg

func Small() int { return 1 }

type Prod struct{ A, B int }

func (p Prod) Read() int { return p.A }
`,
		"pkg/pkg_test.go": `package pkg

import "testing"

type testOnlyStruct struct {
	A, B, C, D, E, F, G, H, I, J int
}

func (testOnlyStruct) Method() {}

func TestHuge(t *testing.T) {
	if true { if true { if true { if true { t.Log("x") } } } }
}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"file", "type", "func"} {
		items, _ := idx.Hotspots(HotspotOptions{Target: target, Limit: 100})
		for _, item := range items {
			if strings.HasSuffix(item.Name, "_test.go") || item.Name == "testOnlyStruct" || item.Name == "TestHuge" {
				t.Fatalf("%s hotspots included test symbol/field: %+v", target, item)
			}
		}
	}
}

func TestTrackedViewRecomputesMetricsAndEdges(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "Test")
	appTracked := `package app

import "example.com/tracked/lib"

func Live() int { return lib.Public() }
`
	appScratch := `package app

import "example.com/tracked/lib"

func Scratch() int { return lib.Hidden() }
func ScratchOnly() {}
`
	libTracked := `package lib

func Public() int { return 1 }
func Hidden() int { return 2 }
`
	libGenerated := `// Code generated by fixture. DO NOT EDIT.

package lib

func Generated() int { return 3 }
`
	libScratch := `// Code generated by fixture. DO NOT EDIT.

package lib

func UntrackedGenerated() int { return 99 }
func UntrackedGeneratedTwo() int { return 100 }
func UntrackedGeneratedThree() int { return 101 }
`
	files := map[string]string{
		"go.mod":               "module example.com/tracked\n\ngo 1.23\n",
		"app/app.go":           appTracked,
		"app/scratch.go":       appScratch,
		"lib/lib.go":           libTracked,
		"lib/generated_gen.go": libGenerated,
		"lib/untracked_gen.go": libScratch,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, root, "add", "go.mod", "app/app.go", "lib/lib.go", "lib/generated_gen.go")

	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	view := idx.TrackedView()
	app := view.Packages["example.com/tracked/app"]
	if app == nil {
		t.Fatal("tracked app package missing")
	}
	if want := countLines([]byte(appTracked)); app.ProdLOC != want || app.Files != 1 || app.Exported != 1 {
		t.Fatalf("app metrics = %+v, want ProdLOC=%d Files=1 Exported=1", app, want)
	}
	lib := view.Packages["example.com/tracked/lib"]
	if lib == nil {
		t.Fatal("tracked lib package missing")
	}
	if want := countLines([]byte(libTracked)); lib.ProdLOC != want {
		t.Fatalf("lib ProdLOC = %d, want %d", lib.ProdLOC, want)
	}
	if lib.GeneratedFiles != 1 || lib.GeneratedLOC != countLines([]byte(libGenerated)) {
		t.Fatalf("lib generated metrics = %+v", lib)
	}
	if lib.Files != 2 || lib.Exported != 3 {
		t.Fatalf("lib file/export metrics = %+v, want Files=2 Exported=3", lib)
	}

	mod := view.Modules["app"]
	if mod == nil {
		t.Fatal("tracked app module missing")
	}
	edge := mod.OutModules["lib"]
	if !slices.Contains(edge, "Public") {
		t.Fatalf("tracked module edge missing Public: %v", edge)
	}
	if slices.Contains(edge, "Hidden") {
		t.Fatalf("untracked source reference survived module edge: %v", edge)
	}
	if !slices.Contains(view.Modules["lib"].InModules["app"], "Public") {
		t.Fatalf("reverse module edge = %v", view.Modules["lib"].InModules)
	}
	if mod.ProdLOC != app.ProdLOC || mod.Files != app.Files || mod.UntrackedFiles != 0 {
		t.Fatalf("app module metrics = %+v", mod)
	}
	if original := idx.Modules["app"]; original.ProdLOC <= mod.ProdLOC {
		t.Fatalf("source module was not over-counting: original=%d tracked=%d", original.ProdLOC, mod.ProdLOC)
	}
}

func TestTestFileMetricsHonorBuildTags(t *testing.T) {
	root := t.TempDir()
	normalTest := `package tags

import "testing"

func TestNormal(t *testing.T) {}
`
	taggedTest := `//go:build integration

package tags

import "testing"

func TestIntegration(t *testing.T) {
	if true {
		t.Log("tagged")
	}
}
`
	writeTagModule(t, root, "example.com/tags", map[string]string{
		"value.go":       "package tags\n\nfunc Value() int { return 1 }\n",
		"value_test.go":  normalTest,
		"tagged_test.go": taggedTest,
		"other/other.go": "package other\n\nfunc Other() int { return 2 }\n",
		"other/other_test.go": `package other

import "testing"

func TestOther(t *testing.T) {}
`,
	})
	plain, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	pkg := plain.Packages["example.com/tags"]
	if pkg == nil {
		t.Fatal("plain package missing")
	}
	if pkg.TestFiles != 1 || pkg.TestLOC != countLines([]byte(normalTest)) {
		t.Fatalf("plain test metrics = %+v", pkg)
	}

	tagged, err := BuildWithOptions(root, BuildOptions{Tags: []string{"integration"}})
	if err != nil {
		t.Fatal(err)
	}
	pkg = tagged.Packages["example.com/tags"]
	if pkg == nil {
		t.Fatal("tagged package missing")
	}
	if pkg.TestFiles != 2 || pkg.TestLOC != countLines([]byte(normalTest))+countLines([]byte(taggedTest)) {
		t.Fatalf("tagged test metrics = %+v", pkg)
	}
}

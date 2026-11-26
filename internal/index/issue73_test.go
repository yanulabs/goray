package index

import (
	"slices"
	"strings"
	"testing"
)

func TestFunctionValueEdgesAndCoverage(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/callbacks", map[string]string{
		"callbacks/callbacks.go": `package callbacks

import (
	"net/http"
	"sort"
)

func handler(w http.ResponseWriter, r *http.Request) {}

func cmp(i, j int) bool { return i < j }

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/x", handler)
	_ = http.HandlerFunc(handler)
}

func SortValues(values []int) { sort.Slice(values, cmp) }

var dispatch = map[string]func(int, int) bool{"cmp": cmp}
`,
		"callbacks/callbacks_test.go": `package callbacks

import (
	"net/http"
	"testing"
)

func TestCallbacks(t *testing.T) {
	Register(http.NewServeMux())
	SortValues([]int{2, 1})
}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	register := "example.com/callbacks/callbacks.Register"
	sortValues := "example.com/callbacks/callbacks.SortValues"
	handler := "example.com/callbacks/callbacks.handler"
	cmp := "example.com/callbacks/callbacks.cmp"
	cases := []struct {
		name string
		from string
		to   string
		kind string
	}{
		{name: "handler registration", from: register, to: handler, kind: "func_value"},
		{name: "handler conversion", from: register, to: handler, kind: "func_value"},
		{name: "sort comparator", from: sortValues, to: cmp, kind: "func_value"},
		{name: "dispatch table", from: "example.com/callbacks/callbacks.dispatch", to: cmp, kind: "func_value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Contains(idx.Callees[tc.from], tc.to) {
				t.Fatalf("callees[%s] missing %s: %v", tc.from, tc.to, idx.Callees[tc.from])
			}
			if !slices.Contains(idx.Callers[tc.to], tc.from) {
				t.Fatalf("callers[%s] missing %s: %v", tc.to, tc.from, idx.Callers[tc.to])
			}
			edge := idx.CallEdgeKinds[tc.from+"\x00"+tc.to]
			if edge.Kind != tc.kind {
				t.Fatalf("edge kind = %+v, want %s", edge, tc.kind)
			}
		})
	}
	for _, id := range []string{handler, cmp} {
		if !idx.TestCovered[id] {
			t.Fatalf("%s should be test covered through func_value edges", id)
		}
	}
	graph := idx.CallGraph(register, "callees", 1, 80)
	found := false
	for _, edge := range graph.Edges {
		if edge.From == register && edge.To == handler && edge.Kind == "func_value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("call graph missing func_value edge: %+v", graph.Edges)
	}
}

func TestPackageLevelInitializersRecordCallsAndLiterals(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/pkginit", map[string]string{
		"initpkg/init.go": `package initpkg

type Config struct {
	Name string
	Port int
}

func loadConfig() Config { return Config{Name: "loaded", Port: 2} }

var Default = Config{Name: "default", Port: 1}
var Loaded = loadConfig()
var _ = helper()

func helper() int { return 1 }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	typeID := "example.com/pkginit/initpkg.Config"
	var froms []string
	for _, site := range idx.CompositeLiterals[typeID] {
		froms = append(froms, site.FromFunc)
	}
	for _, want := range []string{"example.com/pkginit/initpkg.Default", "example.com/pkginit/initpkg.loadConfig"} {
		if !slices.Contains(froms, want) {
			t.Fatalf("composite literals missing FromFunc %s: %v", want, froms)
		}
	}

	loaded := "example.com/pkginit/initpkg.Loaded"
	loadConfig := "example.com/pkginit/initpkg.loadConfig"
	if !slices.Contains(idx.Callees[loaded], loadConfig) {
		t.Fatalf("package-level call edge missing: callees[%s]=%v", loaded, idx.Callees[loaded])
	}
	helper := "example.com/pkginit/initpkg.helper"
	if len(idx.Callers[helper]) == 0 {
		t.Fatalf("blank package-level initializer call not recorded: %+v", idx.Callers)
	}
	if edge := idx.CallEdgeKinds["example.com/pkginit/initpkg.init\x00"+helper]; edge.Kind != "direct" {
		t.Fatalf("blank initializer edge = %+v", edge)
	}
}

func TestMultipleInitFunctionsKeepDistinctIDs(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/multiinit", map[string]string{
		"a.go": `package multiinit

func init() { first() }
func first() {}
`,
		"b.go": `package multiinit

func init() { second() }
func second() {}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	firstInit := idx.Symbols["example.com/multiinit.init"]
	secondInit := idx.Symbols["example.com/multiinit.init#2"]
	if firstInit == nil || secondInit == nil {
		t.Fatalf("init symbols = %v", sortedKeys(idx.Symbols))
	}
	if firstInit.File != "a.go" || secondInit.File != "b.go" {
		t.Fatalf("init order = %s then %s", firstInit.File, secondInit.File)
	}
	if len(idx.SymbolsByName["init"]) != 2 {
		t.Fatalf("init name index = %+v", idx.SymbolsByName["init"])
	}
	if !slices.Contains(idx.Callees[firstInit.ID], "example.com/multiinit.first") {
		t.Fatalf("first init callees = %v", idx.Callees[firstInit.ID])
	}
	if !slices.Contains(idx.Callees[secondInit.ID], "example.com/multiinit.second") {
		t.Fatalf("second init callees = %v", idx.Callees[secondInit.ID])
	}
	for _, canonical := range []string{"sym:example.com/multiinit.init", "sym:example.com/multiinit.init#2"} {
		if idx.Summary.Get(canonical) == nil {
			t.Fatalf("summary missing %s", canonical)
		}
	}

	again, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Symbols["example.com/multiinit.init#2"]; got == nil || got.File != "b.go" || got.Line != secondInit.Line {
		t.Fatalf("init IDs are not deterministic: %+v", got)
	}
}

func TestDeadCodeCanIgnoreTestOnlyReferences(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/testrefs", map[string]string{
		"pkg/prod.go": `package pkg

func onlyFromTest() {}
func usedByProd()   {}
func prodCaller()   { usedByProd() }
func unused()       {}
`,
		"pkg/prod_test.go": `package pkg

import "testing"

func TestLegacy(t *testing.T) {
	onlyFromTest()
	usedByProd()
}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	defaultNames := deadNames(idx.DeadCode(DeadCodeOptions{}))
	if slices.Contains(defaultNames, "onlyFromTest") {
		t.Fatalf("default dead code dropped a test-referenced function: %v", defaultNames)
	}
	ignoreNames := deadNames(idx.DeadCode(DeadCodeOptions{IgnoreTestRefs: true}))
	if !slices.Contains(ignoreNames, "onlyFromTest") {
		t.Fatalf("ignore_test_refs did not report test-only reference: %v", ignoreNames)
	}
	for _, stillLive := range []string{"usedByProd"} {
		if slices.Contains(ignoreNames, stillLive) {
			t.Fatalf("%s has a production reference and must stay live: %v", stillLive, ignoreNames)
		}
	}
	if !slices.Contains(ignoreNames, "unused") || !slices.Contains(ignoreNames, "prodCaller") {
		t.Fatalf("unrelated dead functions missing: %v", ignoreNames)
	}
}

func TestFunctionValueEdgesDoNotDowngradeDirectCalls(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/directkind", map[string]string{
		"a.go": `package directkind

func callee() {}
func caller() { callee() }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	edge := idx.CallEdgeKinds["example.com/directkind.caller\x00example.com/directkind.callee"]
	if edge.Kind != "direct" {
		t.Fatalf("direct call edge = %+v", edge)
	}
	key := "example.com/directkind.caller"
	if !slices.Contains(idx.Callees[key], "example.com/directkind.callee") {
		t.Fatalf("direct callee missing: %v", idx.Callees[key])
	}
	if strings.Count(strings.Join(idx.Callees[key], ","), "example.com/directkind.callee") != 1 {
		t.Fatalf("callee duplicated: %v", idx.Callees[key])
	}
}

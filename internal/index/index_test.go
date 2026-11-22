package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

const (
	samplePayments = "example.com/sample/modules/payments"
	sampleBooking  = "example.com/sample/modules/bookinggw"
)

func TestBuild(t *testing.T) {
	idx, err := Build("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	if idx.ModulePath != "example.com/sample" {
		t.Fatalf("module path = %q", idx.ModulePath)
	}
	if len(idx.Packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(idx.Packages))
	}
	if len(idx.LoadErrors) != 0 {
		t.Fatalf("load errors: %v", idx.LoadErrors)
	}

	chargeID := samplePayments + ".Service.Charge"
	charge := idx.Symbols[chargeID]
	if charge == nil {
		t.Fatalf("missing %s", chargeID)
	}
	if charge.Kind != "method" || charge.RecvType != "Service" || !charge.Exported {
		t.Fatalf("unexpected symbol: %+v", charge)
	}
	if charge.Cyclomatic != 1 || charge.EndLine < charge.Line {
		t.Fatalf("bad metrics: %+v", charge)
	}

	impls := idx.IfaceImpls[samplePayments+".Gateway"]
	if len(impls) != 1 || impls[0].TypeID != samplePayments+".Service" || !impls[0].ViaPtr {
		t.Fatalf("implementations = %+v", impls)
	}

	mainID := sampleBooking + ".main"
	if !slices.Contains(idx.Callers[chargeID], mainID) {
		t.Fatalf("callers of Charge = %v", idx.Callers[chargeID])
	}
	if len(idx.XRefs[samplePayments+".NewService"]) == 0 {
		t.Fatal("NewService has no references")
	}

	payments := idx.Packages[samplePayments]
	if payments.ProdLOC == 0 || payments.TestFiles != 1 || payments.TestLOC == 0 {
		t.Fatalf("package metrics = %+v", payments)
	}
	if payments.Goroutines != 1 {
		t.Fatalf("goroutines = %d, want 1", payments.Goroutines)
	}

	booking := idx.Modules["bookinggw"]
	if booking == nil || !slices.Contains(booking.OutModules["payments"], "Charge") {
		t.Fatalf("cross-module deps = %+v", booking)
	}
	if !slices.Contains(idx.Modules["payments"].InModules["bookinggw"], "Charge") {
		t.Fatalf("reverse deps = %+v", idx.Modules["payments"].InModules)
	}

	dead := idx.DeadFunctions("", true)
	if !slices.ContainsFunc(dead, func(s *Symbol) bool { return s.Name == "unusedHelper" }) {
		t.Fatalf("unusedHelper missing from dead code: %v", dead)
	}
	if slices.ContainsFunc(dead, func(s *Symbol) bool { return s.Name == "Charge" }) {
		t.Fatal("Charge must not be dead")
	}
}

func TestBuildStandardLayout(t *testing.T) {
	idx, err := Build("testdata/standard")
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Packages) != 2 || len(idx.Modules) != 2 {
		t.Fatalf("packages=%d modules=%d", len(idx.Packages), len(idx.Modules))
	}

	core := idx.Modules["internal/core"]
	if core == nil {
		t.Fatalf("modules = %v", idx.SortedModuleNames())
	}
	if core.Packages != 1 || core.RootExported != 4 {
		t.Fatalf("core summary = %+v", core)
	}
	if idx.Packages["example.com/standard/internal/core"].ModuleArea != "internal/core" {
		t.Fatalf("package area = %q", idx.Packages["example.com/standard/internal/core"].ModuleArea)
	}

	app := idx.Modules["cmd/app"]
	if app == nil || !slices.Contains(app.OutModules["internal/core"], "Greet") {
		t.Fatalf("app deps = %+v", app)
	}
	if !slices.Contains(core.InModules["cmd/app"], "New") {
		t.Fatalf("core callers = %+v", core.InModules)
	}
}

func TestQueries(t *testing.T) {
	idx, err := Build("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}

	if pkg, ok := idx.ResolvePackage("payments"); !ok || pkg != samplePayments {
		t.Fatalf("ResolvePackage = %q, %v", pkg, ok)
	}
	if _, ok := idx.ResolvePackage("missing"); ok {
		t.Fatal("unexpected package resolution")
	}

	sym, err := idx.SingleSymbol("Charge")
	if err != nil || sym.ID != samplePayments+".Service.Charge" {
		t.Fatalf("SingleSymbol = %v, %v", sym, err)
	}
	if got, err := idx.SingleSymbol(sym.CanonicalID()); err != nil || got.ID != sym.ID {
		t.Fatalf("SingleSymbol(canonical) = %v, %v", got, err)
	}
	if matches := idx.Resolve(sym.CanonicalID(), "", 5); len(matches) != 1 || matches[0].ID != sym.ID {
		t.Fatalf("Resolve(canonical) = %v", matches)
	}
	if _, err := idx.SingleSymbol(""); err == nil {
		t.Fatal("empty query should fail")
	}

	callers := idx.CallGraph(sym.ID, "callers", 2, 80)
	if callers.Nodes != 3 || len(callers.Edges) != 2 {
		t.Fatalf("call graph = %+v", callers)
	}
	var sawTestCaller bool
	for _, edge := range callers.Edges {
		if edge.From == samplePayments+".TestServiceCharge" {
			sawTestCaller = true
			if edge.Kind != "direct" {
				t.Fatalf("test caller edge kind = %q", edge.Kind)
			}
		}
	}
	if !sawTestCaller {
		t.Fatalf("test caller missing from call graph: %+v", callers)
	}

	dep := idx.DependencyGraph(samplePayments, "in", 1)
	if dep.Nodes != 2 || len(dep.Edges) != 1 || dep.Edges[0].From != "modules/bookinggw" {
		t.Fatalf("dependency graph = %+v", dep)
	}
	both := idx.DependencyGraph(samplePayments, "both", 2)
	if len(both.Edges) != 1 {
		t.Fatalf("both-direction graph should not duplicate edges: %+v", both)
	}

	impact := idx.ImpactBySymbol(sym, 3)
	if impact.Packages < 2 || !slices.Contains(impact.Modules, "bookinggw") {
		t.Fatalf("impact = %+v", impact)
	}
	if !slices.Contains(impact.TestPackages, "./modules/payments") {
		t.Fatalf("test packages = %v", impact.TestPackages)
	}
}

func TestGeneratedRollups(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/gen", map[string]string{
		"normal.go": `package gen

func Normal() int { return 1 }
`,
		"dto.gen.go": `// Code generated by fixture. DO NOT EDIT.
package gen

type DTO string

func NewDTO() DTO { return "" }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	pkg := idx.Packages["example.com/gen"]
	if pkg == nil {
		t.Fatalf("packages = %v", idx.Packages)
	}
	if pkg.GeneratedFiles != 1 || pkg.GeneratedSymbols != 2 || pkg.GeneratedLOC == 0 {
		t.Fatalf("generated rollups = %+v", pkg)
	}
	if pkg.ProdLOC == 0 {
		t.Fatalf("normal file should contribute prod loc: %+v", pkg)
	}
	mod := idx.Modules[pkg.ModuleArea]
	if mod == nil {
		t.Fatalf("module %q missing; modules = %v", pkg.ModuleArea, idx.SortedModuleNames())
	}
	if mod.GeneratedFiles != 1 || mod.GeneratedSymbols != 2 || mod.GeneratedLOC != pkg.GeneratedLOC {
		t.Fatalf("module generated rollups = %+v", mod)
	}
	for _, name := range []string{"DTO", "NewDTO"} {
		sym := idx.Symbols["example.com/gen."+name]
		if sym == nil || !sym.Generated {
			t.Fatalf("%s generated flag = %+v", name, sym)
		}
	}
	if normal := idx.Symbols["example.com/gen.Normal"]; normal == nil || normal.Generated {
		t.Fatalf("normal generated flag = %+v", normal)
	}
}

func TestTolerantSymbolResolution(t *testing.T) {
	idx, err := Build("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	charge := idx.Symbols[samplePayments+".Service.Charge"]
	if charge == nil {
		t.Fatal("missing Charge")
	}
	for _, query := range []string{
		charge.CanonicalID(),
		"example.com/sample/modules/payments.Service.Charge",
		"modules/payments.Service.Charge",
		"Service.Charge",
		"sym:example.com/sample/modules/payments.Service.Charge",
	} {
		got, err := idx.SingleSymbol(query)
		if err != nil || got.ID != charge.ID {
			t.Fatalf("SingleSymbol(%q) = %v, %v", query, got, err)
		}
	}
	for _, query := range []string{
		"modules/payments",
		"pkg:example.com/sample/modules/payments",
		"mod:payments",
	} {
		if pkg, ok := idx.ResolvePackage(query); !ok || pkg != samplePayments {
			t.Fatalf("ResolvePackage(%q) = %q, %v", query, pkg, ok)
		}
	}
	if name, ok := idx.ResolveModule("mod:payments"); !ok || name != "payments" {
		t.Fatalf("ResolveModule = %q, %v", name, ok)
	}
}

func TestAmbiguousShorthandAndNotFound(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/ambig", map[string]string{
		"a/a.go": "package a\n\ntype Thing struct{}\n",
		"b/b.go": "package b\n\ntype Thing struct{}\n",
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.SingleSymbol("Thing"); err == nil {
		t.Fatal("expected ambiguous shorthand error")
	} else if ambiguous, ok := err.(*AmbiguousSymbolError); !ok || len(ambiguous.IDs) != 2 {
		t.Fatalf("error = %T %v", err, err)
	}
	if got, err := idx.SingleSymbol("a.Thing"); err != nil || got.ID != "example.com/ambig/a.Thing" {
		t.Fatalf("a.Thing = %v, %v", got, err)
	}
	if _, err := idx.SingleSymbol("a.Missing"); err == nil {
		t.Fatal("expected not found")
	} else if _, ok := err.(*SymbolNotFoundError); !ok {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestAstHelpers(t *testing.T) {
	if got := countLines([]byte("a\nb")); got != 2 {
		t.Fatalf("countLines = %d", got)
	}
	if !isGenerated("x.go", []byte("// Code generated by tool. DO NOT EDIT.")) {
		t.Fatal("generated marker missed")
	}

	for _, tc := range []struct {
		rel  string
		name string
		root bool
	}{
		{"modules/payments", "payments", true},
		{"modules/payments/api", "payments", false},
		{"internal/index", "internal/index", true},
		{"internal/index/sub", "internal/index", false},
		{"cmd/app", "cmd/app", true},
		{"pkg/foo/bar", "pkg/foo", false},
		{"api/v1", "api", false},
		{".", "root", true},
	} {
		name, root := moduleArea(tc.rel)
		if name != tc.name || root != tc.root {
			t.Fatalf("moduleArea(%q) = %q, %v; want %q, %v", tc.rel, name, root, tc.name, tc.root)
		}
	}

	src := `package p
func f(a, b bool) int {
	if a && b {
		return 1
	}
	for range []int{1} {}
	return 2
}`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if got := cyclomatic(fn.Body); got != 4 {
		t.Fatalf("cyclomatic = %d, want 4", got)
	}
}

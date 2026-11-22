package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/sig\n\ngo 1.23\n"
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
}

func buildTestModule(t *testing.T, files map[string]string) (*Index, string) {
	t.Helper()
	dir := t.TempDir()
	writeModule(t, dir, files)
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return idx, dir
}

func summaryOf(t *testing.T, idx *Index, id string) *SummarySymbol {
	t.Helper()
	sym := idx.Summary.Get(id)
	if sym == nil {
		t.Fatalf("missing summary symbol %s", id)
	}
	return sym
}

func TestSummaryCanonicalIDsAndEligibility(t *testing.T) {
	idx, _ := buildTestModule(t, map[string]string{
		"a/a.go": `package a

type OrderID int64

type Gateway interface {
	Do()
}

type Service struct{}

func (s *Service) Do() {}

func (s *Service) Run(id OrderID) OrderID {
	if id > 0 {
		return id
	}
	return 0
}

func (s Service) Value() {}

func Use(id OrderID) OrderID { return id }

func small() {}

func Big() int {
	if true { return 1 }
	if true { return 2 }
	if true { return 3 }
	if true { return 4 }
	if true { return 5 }
	if true { return 6 }
	if true { return 7 }
	if true { return 8 }
	if true { return 9 }
	if true { return 10 }
	return 0
}
`,
		"b/b.go": `package b

import "example.com/sig/a"

func Caller() a.OrderID {
	return a.Use(1)
}
`,
	})

	order := summaryOf(t, idx, "sym:example.com/sig/a.OrderID")
	use := summaryOf(t, idx, "sym:example.com/sig/a.Use")
	run := summaryOf(t, idx, "sym:example.com/sig/a.(*Service).Run")
	value := summaryOf(t, idx, "sym:example.com/sig/a.(Service).Value")
	iface := summaryOf(t, idx, "sym:example.com/sig/a.Gateway")
	small := summaryOf(t, idx, "sym:example.com/sig/a.small")
	big := summaryOf(t, idx, "sym:example.com/sig/a.Big")
	caller := summaryOf(t, idx, "sym:example.com/sig/b.Caller")
	file := summaryOf(t, idx, "file:a/a.go")
	pkg := summaryOf(t, idx, "pkg:example.com/sig/a")
	mod := summaryOf(t, idx, "mod:a")

	if run.Kind != "method" || value.Kind != "method" {
		t.Fatalf("method kinds = %q, %q", run.Kind, value.Kind)
	}
	if iface.Kind != "type" || order.Kind != "type" {
		t.Fatalf("type kinds = %q, %q", iface.Kind, order.Kind)
	}
	if run.ParentID != file.ID || file.ParentID != pkg.ID || pkg.ParentID != mod.ID {
		t.Fatalf("parent chain: %s -> %s -> %s -> %s", run.ParentID, file.ParentID, pkg.ParentID, mod.ID)
	}
	if mod.SelfHash != "" || mod.DepHash == "" || mod.ContentHash == "" {
		t.Fatalf("module hashes: %+v", mod)
	}

	if order.Eligible || !iface.Eligible || !big.Eligible {
		t.Fatalf("expected interface/complex symbols eligible and simple type ineligible: %+v %+v %+v", order, iface, big)
	}
	if small.Eligible || use.Eligible {
		t.Fatalf("trivial symbols must not be eligible: %+v %+v", small, use)
	}
	if file.Eligible || !pkg.Eligible || !mod.Eligible {
		t.Fatalf("file/pkg/module eligibility: %v %v %v", file.Eligible, pkg.Eligible, mod.Eligible)
	}
	if use.FanIn == 0 || caller.FanIn != 0 {
		t.Fatalf("fan-in: use=%d caller=%d", use.FanIn, caller.FanIn)
	}

	var calls, implements, imports bool
	for _, edge := range idx.Summary.Edges {
		switch {
		case edge.Kind == "calls" && edge.SrcID == caller.ID && edge.DstID == use.ID:
			calls = true
		case edge.Kind == "implements" && edge.SrcID == "sym:example.com/sig/a.Service" && edge.DstID == iface.ID:
			implements = true
		case edge.Kind == "imports" && edge.SrcID == "pkg:example.com/sig/b" && edge.DstID == pkg.ID:
			imports = true
		}
	}
	if !calls || !implements || !imports {
		t.Fatalf("summary edges missing calls=%v implements=%v imports=%v", calls, implements, imports)
	}
}

func TestSummaryHashInvariance(t *testing.T) {
	original := `package a

//go:noinline
func Target(x OrderID) OrderID {
	if x > 0 {
		return x
	}
	return 0
}

func Caller() OrderID { return Target(1) }

func UsePayload(x Payload) {}
`
	reformatted := `package a

// ordinary comment changed

//go:noinline
func Target(x OrderID) OrderID {

	// inline comment changed
	if x > 0 {
		return x
	}

	return 0
}

func Caller() OrderID {
	return Target(1)
}

func UsePayload(x Payload) {}
`
	files := map[string]string{
		"a/a.go":     original,
		"a/types.go": "package a\ntype OrderID int64\ntype Payload int64\n",
	}
	idx, dir := buildTestModule(t, files)
	targetBefore := *summaryOf(t, idx, "sym:example.com/sig/a.Target")
	callerBefore := *summaryOf(t, idx, "sym:example.com/sig/a.Caller")
	useBefore := *summaryOf(t, idx, "sym:example.com/sig/a.UsePayload")

	writeModule(t, dir, map[string]string{
		"a/a.go":     reformatted,
		"a/types.go": "package a\ntype OrderID int64\ntype Payload int64\n",
	})
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.Target"); got.SelfHash != targetBefore.SelfHash || got.DepHash != targetBefore.DepHash || got.ContentHash != targetBefore.ContentHash {
		t.Fatalf("reformat changed Target hashes:\nbefore %+v\nafter %+v", targetBefore, *got)
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.Caller"); got.ContentHash != callerBefore.ContentHash {
		t.Fatalf("reformat changed Caller content hash")
	}

	// Directive edits must invalidate the declaration hash.
	writeModule(t, dir, map[string]string{
		"a/a.go":     strings.Replace(reformatted, "//go:noinline", "//go:nosplit", 1),
		"a/types.go": "package a\ntype OrderID int64\ntype Payload int64\n",
	})
	idx, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.Target"); got.SelfHash == targetBefore.SelfHash {
		t.Fatal("directive change did not invalidate self_hash")
	}

	// A named type's underlying change must invalidate references across files.
	writeModule(t, dir, map[string]string{
		"a/a.go":     reformatted,
		"a/types.go": "package a\ntype OrderID int64\ntype Payload string\n",
	})
	idx, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.UsePayload"); got.ContentHash == useBefore.ContentHash {
		t.Fatal("named type underlying change did not invalidate referencing function")
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.Caller"); got.ContentHash != callerBefore.ContentHash {
		t.Fatal("unrelated named type change invalidated caller")
	}
}

func TestSummaryCycleSafety(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

func A(n int) int {
	if n == 0 {
		return 0
	}
	return B(n - 1)
}

func B(n int) int {
	if n == 0 {
		return 1
	}
	return A(n - 1)
}
`}
	idx, dir := buildTestModule(t, files)
	aBefore := *summaryOf(t, idx, "sym:example.com/sig/a.A")
	bBefore := *summaryOf(t, idx, "sym:example.com/sig/a.B")
	fileBefore := *summaryOf(t, idx, "file:a/a.go")
	pkgBefore := *summaryOf(t, idx, "pkg:example.com/sig/a")

	edited := strings.Replace(files["a/a.go"], "return B(n - 1)", "return B(n-1) + 1", 1)
	writeModule(t, dir, map[string]string{"a/a.go": edited})
	idx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	aAfter := summaryOf(t, idx, "sym:example.com/sig/a.A")
	bAfter := summaryOf(t, idx, "sym:example.com/sig/a.B")
	if aAfter.ContentHash == aBefore.ContentHash {
		t.Fatal("leaf body edit did not invalidate leaf")
	}
	if bAfter.ContentHash != bBefore.ContentHash {
		t.Fatal("leaf body edit cascaded into mutual caller")
	}
	if summaryOf(t, idx, "file:a/a.go").ContentHash == fileBefore.ContentHash {
		t.Fatal("file rollup did not change")
	}
	if summaryOf(t, idx, "pkg:example.com/sig/a").ContentHash == pkgBefore.ContentHash {
		t.Fatal("package rollup did not change")
	}
}

func TestGeneratedAndTestFilesNotEligible(t *testing.T) {
	big := `func %s() int {
	if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return 1 } } } } } } } } } }
	return 0
}
`
	idx, _ := buildTestModule(t, map[string]string{
		"a/a.go":         "package a\n\n" + strings.Replace(big, "%s", "Real", 1),
		"a/generated.go": "// Code generated by tool. DO NOT EDIT.\n\npackage a\n\n" + strings.Replace(big, "%s", "GeneratedBig", 1),
		"a/a_test.go":    "package a\n\n" + strings.Replace(big, "%s", "TestBig", 1),
	})
	real := summaryOf(t, idx, "sym:example.com/sig/a.Real")
	generated := summaryOf(t, idx, "sym:example.com/sig/a.GeneratedBig")
	testFn := summaryOf(t, idx, "sym:example.com/sig/a.TestBig")
	if !real.Eligible {
		t.Fatal("normal function should be eligible")
	}
	if generated.Eligible || testFn.Eligible {
		t.Fatalf("generated/test functions must not be eligible: %v %v", generated.Eligible, testFn.Eligible)
	}
	if summaryOf(t, idx, "file:a/generated.go").Eligible || summaryOf(t, idx, "file:a/a_test.go").Eligible {
		t.Fatal("generated/test files must not be eligible")
	}
	seen := map[string]bool{}
	for _, sym := range idx.Summary.Symbols {
		if seen[sym.ID] {
			t.Fatalf("duplicate summary symbol: %s", sym.ID)
		}
		seen[sym.ID] = true
	}
	if len(idx.Summary.ByID) != len(idx.Summary.Symbols) {
		t.Fatalf("summary map/slice mismatch: %d vs %d", len(idx.Summary.ByID), len(idx.Summary.Symbols))
	}
}

func TestCycleSafeTypeHashing(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

type Node struct { Next *Node }

type A struct { B *B }
type B struct { A *A }

type Ordered[T Ordered[T]] interface {
	Less(T) bool
}

type Concrete struct { N Node }
type Alias = Concrete
type Defined Concrete
`}
	idx, dir := buildTestModule(t, files)
	ids := []string{
		"sym:example.com/sig/a.Node",
		"sym:example.com/sig/a.A",
		"sym:example.com/sig/a.B",
		"sym:example.com/sig/a.Ordered",
		"sym:example.com/sig/a.Concrete",
		"sym:example.com/sig/a.Alias",
		"sym:example.com/sig/a.Defined",
	}
	before := map[string]string{}
	for _, id := range ids {
		sym := summaryOf(t, idx, id)
		if sym.SigHash == "" || sym.SelfHash == "" || sym.ContentHash == "" {
			t.Fatalf("%s has empty hash: %+v", id, sym)
		}
		before[id] = sym.ContentHash
	}
	if before["sym:example.com/sig/a.Alias"] == before["sym:example.com/sig/a.Defined"] {
		t.Fatal("alias and defined type must hash differently")
	}
	if before["sym:example.com/sig/a.A"] == before["sym:example.com/sig/a.B"] {
		t.Fatal("mutually recursive types must retain distinct identities")
	}

	if _, err := Build(dir); err != nil {
		t.Fatal(err)
	}
	idx2, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range before {
		if got := summaryOf(t, idx2, id).ContentHash; got != want {
			t.Fatalf("%s hash not deterministic: %s != %s", id, got, want)
		}
	}
}

func TestBuildRejectsTypeErrors(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{"a/a.go": "package a\n\nfunc Broken() int { return missingName }\n"})
	if _, err := Build(dir); err == nil {
		t.Fatal("type error should fail the build")
	} else {
		var loadErr *LoadError
		if !errors.As(err, &loadErr) || len(loadErr.Errors) == 0 {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestEligibilityPolicyFunctionsAndTypes(t *testing.T) {
	longBody := strings.Repeat("\t// filler\n", 60)
	complexBody := "if true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return 1 } } } } } } } } } }\n\treturn 0"
	callers := ""
	for i := 0; i < 8; i++ {
		callers += fmt.Sprintf("func Call%d() { _ = Popular() }\n", i)
	}
	idx, _ := buildTestModule(t, map[string]string{"a/a.go": fmt.Sprintf(`package a

type ExportedInterface interface { M() }
type unexportedInterface interface { M() }
type ExportedSimple int
type ExportedStruct struct { A, B, C, D, E, F int }
type unexportedStruct struct { A, B, C, D, E, F int }
type ExportedMethods struct{}

func (ExportedMethods) M1() {}
func (ExportedMethods) M2() {}
func (ExportedMethods) M3() {}

func Small() int { return 1 }
func Popular() int { return 2 }
%s
func Complex() int {
	%s
}

func Long() int {
%s	return 1
}
`, callers, complexBody, longBody)})

	cases := map[string]bool{
		"ExportedInterface":   true,
		"unexportedInterface": false,
		"ExportedSimple":      false,
		"ExportedStruct":      true,
		"unexportedStruct":    false,
		"ExportedMethods":     true,
		"Small":               false,
		"Popular":             true,
		"Complex":             true,
		"Long":                true,
	}
	for name, want := range cases {
		sym := summaryOf(t, idx, "sym:example.com/sig/a."+name)
		if sym.Eligible != want {
			t.Fatalf("%s eligible = %v, want %v (%+v)", name, sym.Eligible, want, sym)
		}
	}
}

func TestEligibilityPolicyFilesPackagesModules(t *testing.T) {
	big := "func %s() int {\n\tif true { if true { if true { if true { if true { if true { if true { if true { if true { if true { return 1 } } } } } } } } } }\n\treturn 0\n}\n"
	large := "package f\n\n" + strings.Repeat("// filler\n", 260) +
		fmt.Sprintf(big, "F1") + fmt.Sprintf(big, "F2") + fmt.Sprintf(big, "F3")
	longOnly := "package f\n\n" + strings.Repeat("// filler\n", 260) + fmt.Sprintf(big, "F4")
	small := "package f\n\n" + fmt.Sprintf(big, "F5") + fmt.Sprintf(big, "F6") + fmt.Sprintf(big, "F7")
	idx, _ := buildTestModule(t, map[string]string{
		"f/large.go":            large,
		"f/longonly.go":         longOnly,
		"f/small.go":            small,
		"one/one.go":            "package one\n\nfunc One() int { return 1 }\n",
		"two/a/a.go":            "package twoa\n\nfunc A() int { return 1 }\n",
		"two/b/b.go":            "package twob\n\nfunc B() int { return 1 }\n",
		"gen/g/generated.go":    "// Code generated by tool. DO NOT EDIT.\n\npackage g\n\nfunc G() int { return 1 }\n",
		"gen/h/h.go":            "package h\n\nfunc H() int { return 1 }\n",
		"onlytest/only_test.go": "package onlytest\n\nimport \"testing\"\n\nfunc TestOnly(t *testing.T) {}\n",
	})

	if !summaryOf(t, idx, "file:f/large.go").Eligible {
		t.Fatal("large file with >=3 eligible symbols should be eligible")
	}
	if summaryOf(t, idx, "file:f/small.go").Eligible {
		t.Fatal("short file should not be eligible")
	}
	if summaryOf(t, idx, "file:f/longonly.go").Eligible {
		t.Fatal("long file with <3 eligible symbols should not be eligible")
	}
	if summaryOf(t, idx, "pkg:example.com/sig/f").Eligible != true {
		t.Fatal("package with production files should be eligible")
	}
	if summaryOf(t, idx, "pkg:example.com/sig/gen/g").Eligible {
		t.Fatal("generated-only package should not be eligible")
	}
	if !summaryOf(t, idx, "pkg:example.com/sig/gen/h").Eligible {
		t.Fatal("normal package should be eligible")
	}
	if !summaryOf(t, idx, "mod:f").Eligible {
		t.Fatal("single-package module with production files should be eligible")
	}
	if !summaryOf(t, idx, "mod:one").Eligible {
		t.Fatal("single-package module with production files should be eligible")
	}
	if !summaryOf(t, idx, "mod:two").Eligible || !summaryOf(t, idx, "mod:gen").Eligible {
		t.Fatal("multi-package modules should be eligible")
	}
	if summaryOf(t, idx, "mod:onlytest").Eligible {
		t.Fatal("test-only module should not be eligible")
	}
}

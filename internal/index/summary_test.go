package index

import (
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

	if !order.Eligible || !iface.Eligible || !big.Eligible {
		t.Fatalf("expected exported/interface/complex symbols eligible: %+v %+v %+v", order, iface, big)
	}
	if small.Eligible || use.Eligible {
		t.Fatalf("trivial symbols must not be eligible: %+v %+v", small, use)
	}
	if !file.Eligible || !pkg.Eligible || mod.Eligible != true {
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

func UseType(x OrderID) {}
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

func UseType(x OrderID) {}
`
	files := map[string]string{
		"a/a.go":     original,
		"a/types.go": "package a\ntype OrderID int64\n",
	}
	idx, dir := buildTestModule(t, files)
	targetBefore := *summaryOf(t, idx, "sym:example.com/sig/a.Target")
	callerBefore := *summaryOf(t, idx, "sym:example.com/sig/a.Caller")
	useBefore := *summaryOf(t, idx, "sym:example.com/sig/a.UseType")

	writeModule(t, dir, map[string]string{
		"a/a.go":     reformatted,
		"a/types.go": "package a\ntype OrderID int64\n",
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
		"a/types.go": "package a\ntype OrderID int64\n",
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
		"a/types.go": "package a\ntype OrderID string\n",
	})
	idx, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.UseType"); got.ContentHash == useBefore.ContentHash {
		t.Fatal("named type underlying change did not invalidate referencing function")
	}
	if got := summaryOf(t, idx, "sym:example.com/sig/a.Caller"); got.ContentHash == callerBefore.ContentHash {
		t.Fatal("named type underlying change did not invalidate caller signature")
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

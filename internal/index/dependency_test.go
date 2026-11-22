package index

import (
	"slices"
	"strings"
	"testing"
)

func TestExplainDependencyPackageAndBlankImport(t *testing.T) {
	idx := buildPortsFixture(t)

	explanation, err := idx.ExplainDependency("checkout", "ports", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.FromKind != "module" || explanation.ToKind != "module" {
		t.Fatalf("kinds = %q -> %q", explanation.FromKind, explanation.ToKind)
	}
	if len(explanation.Edges) != 1 {
		t.Fatalf("edges = %+v", explanation.Edges)
	}
	edge := explanation.Edges[0]
	if edge.FromPkg != "example.com/ports/checkout" || edge.ToPkg != "example.com/ports/ports" {
		t.Fatalf("edge = %+v", edge)
	}
	if len(edge.Files) != 1 || edge.Files[0].File != "checkout/checkout.go" || edge.Files[0].Line == 0 {
		t.Fatalf("files = %+v", edge.Files)
	}
	if !slices.ContainsFunc(edge.Symbols, func(sym DependencySymbol) bool {
		return sym.Name == "Charger" && len(sym.Usages) > 0 && sym.Usages[0].File != ""
	}) {
		t.Fatalf("symbols = %+v", edge.Symbols)
	}
	if explanation.Summary.Files != 1 || explanation.Summary.UsageSites == 0 {
		t.Fatalf("summary = %+v", explanation.Summary)
	}

	blank, err := idx.ExplainDependency("example.com/ports/checkout", "sideeffect", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if blank.FromKind != "package" || blank.ToKind != "module" {
		t.Fatalf("kinds = %q -> %q", blank.FromKind, blank.ToKind)
	}
	if len(blank.Edges) != 1 || len(blank.Edges[0].Files) != 1 || !blank.Edges[0].Files[0].Blank {
		t.Fatalf("blank import = %+v", blank.Edges)
	}
	if len(blank.Edges[0].Symbols) != 0 {
		t.Fatalf("blank import symbols = %+v", blank.Edges[0].Symbols)
	}
	if !strings.Contains(blank.Edges[0].Files[0].File, "checkout.go") {
		t.Fatalf("blank import file = %+v", blank.Edges[0].Files[0])
	}
}

func TestExplainDependencyIncludeTestsAndReverseHint(t *testing.T) {
	idx := buildPortsFixture(t)

	without, err := idx.ExplainDependency("payments", "checkout", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Edges) != 0 || !strings.Contains(without.Hint, "reverse") {
		t.Fatalf("reverse hint = %+v", without)
	}

	withTests, err := idx.ExplainDependency("checkout", "payments", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(withTests.Edges) != 1 || !slices.ContainsFunc(withTests.Edges[0].Files, func(file DependencyFile) bool {
		return file.Test && strings.HasSuffix(file.File, "_test.go")
	}) {
		t.Fatalf("test edges = %+v", withTests.Edges)
	}
	if !slices.ContainsFunc(withTests.Edges[0].Symbols, func(sym DependencySymbol) bool { return sym.Name == "Version" }) {
		t.Fatalf("test symbols = %+v", withTests.Edges[0].Symbols)
	}

	excluded, err := idx.ExplainDependency("checkout", "payments", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(excluded.Edges) != 0 {
		t.Fatalf("test-only edge included without include_tests: %+v", excluded.Edges)
	}

	limited, err := idx.ExplainDependency("checkout", "ports", false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated {
		t.Fatalf("limit did not truncate: %+v", limited)
	}
	if limited.Summary.UsageSites == 0 {
		t.Fatalf("summary totals must describe the full edge: %+v", limited.Summary)
	}
}

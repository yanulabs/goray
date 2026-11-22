package index

import (
	"slices"
	"testing"
)

func TestAliasFieldsKeepDeclaringTypeOwnership(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/alias", map[string]string{
		"contracts/plan.go": `package contracts

type Plan struct {
	Price int
}
`,
		"billing/plan.go": `package billing

import "example.com/alias/contracts"

type Plan = contracts.Plan

func Make() contracts.Plan { return contracts.Plan{Price: 1} }
`,
		"checkout/use.go": `package checkout

import "example.com/alias/billing"

func Read(p billing.Plan) int { return p.Price }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if sym := idx.Symbols["example.com/alias/billing.Plan.Price"]; sym != nil {
		t.Fatalf("alias package re-indexed original struct fields: %+v", sym)
	}
	original := idx.Symbols["example.com/alias/contracts.Plan.Price"]
	if original == nil {
		t.Fatal("original field symbol missing")
	}
	refs := idx.XRefs[original.ID]
	if !slices.ContainsFunc(refs, func(ref XRef) bool {
		return ref.FromPkg == "example.com/alias/checkout" && ref.File == "checkout/use.go"
	}) {
		t.Fatalf("alias field usage attributed to the wrong symbol: %+v", refs)
	}
	if cycles := idx.ModuleCycles(); len(cycles) != 0 {
		t.Fatalf("alias created false module cycles: %v", cycles)
	}
	if mod := idx.Modules["billing"]; mod == nil || len(mod.OutModules["contracts"]) == 0 {
		t.Fatalf("billing -> contracts production dependency missing: %+v", mod)
	}
}

func TestTestReferencesDoNotCreateArchitectureEdges(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/testedges", map[string]string{
		"alpha/alpha.go": `package alpha

func Value() int { return 1 }
`,
		"beta/beta.go": `package beta

func Local() int { return 2 }
`,
		"beta/beta_test.go": `package beta

import (
	"testing"

	"example.com/testedges/alpha"
)

func TestCross(t *testing.T) { _ = alpha.Value() }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	beta := idx.Modules["beta"]
	if beta == nil {
		t.Fatal("missing beta module")
	}
	if len(beta.OutModules["alpha"]) != 0 {
		t.Fatalf("test-only dependency leaked into architecture edges: %+v", beta.OutModules)
	}
	if cycles := idx.ModuleCycles(); len(cycles) != 0 {
		t.Fatalf("test-only imports created cycles: %v", cycles)
	}
	refs := idx.XRefs["example.com/testedges/alpha.Value"]
	if !slices.ContainsFunc(refs, func(ref XRef) bool { return ref.Test && ref.FromPkg == "example.com/testedges/beta" }) {
		t.Fatalf("test reference lost from XRefs: %+v", refs)
	}
	if !slices.ContainsFunc(idx.ImportSites, func(site ImportSite) bool {
		return site.Test && site.FromPkg == "example.com/testedges/beta" && site.ToPkg == "example.com/testedges/alpha"
	}) {
		t.Fatalf("test import site lost: %+v", idx.ImportSites)
	}
}

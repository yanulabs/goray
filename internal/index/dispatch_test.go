package index

import (
	"slices"
	"strings"
	"testing"
)

func buildPortsFixture(t *testing.T) *Index {
	t.Helper()
	root := t.TempDir()
	writeTagModule(t, root, "example.com/ports", map[string]string{
		"ports/ports.go": `package ports

type Charger interface {
	Charge(amount int) error
}

type Extended interface {
	Charger
	Refund() error
}

type Generic[T any] struct{ Value T }

func (g Generic[T]) Charge(int) error { return nil }

type Inline interface {
	interface{ Ping() error }
}
`,
		"payments/payments.go": `package payments

type Module struct{}

func (m *Module) Charge(amount int) error { return nil }

func Version() string { return "1" }
`,
		"checkout/checkout.go": `package checkout

import (
	"example.com/ports/ports"
	_ "example.com/ports/sideeffect"
)

type Service struct {
	port ports.Charger
}

func New(p ports.Charger) *Service { return &Service{port: p} }

func (s *Service) Pay(amount int) error { return s.port.Charge(amount) }
`,
		"sideeffect/side.go": `package sideeffect

func init() {}
`,
		"checkout/checkout_test.go": `package checkout

import (
	"errors"
	"testing"

	"example.com/ports/payments"
)

type mockCharger struct{}

func (mockCharger) Charge(int) error { return errors.New("mock") }

func TestPay(t *testing.T) {
	service := New(mockCharger{})
	if err := service.Pay(1); err == nil {
		t.Fatal("expected error")
	}
	_ = payments.Version()
}
`,
		"models/models.go": `package models

type Order struct {
	ID     int
	Status string
}

func NewOrder(id int) Order { return Order{ID: id} }

func (o Order) Inspect() string { return o.Status }

type Other struct{ ID int }

func UseOther(o Other) int { return o.ID }
`,
		"models/errors.go": `package models

import "errors"

var ErrNotFound = errors.New("not found")

func IsMissing(err error) bool { return err == ErrNotFound }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestInterfaceDispatchCallGraph(t *testing.T) {
	idx := buildPortsFixture(t)
	chargeID := "example.com/ports/payments.Module.Charge"
	serviceID := "example.com/ports/checkout.Service.Pay"

	if !slices.Contains(idx.Callers[chargeID], serviceID) {
		t.Fatalf("interface dispatch missing from callers: %v", idx.Callers[chargeID])
	}
	edge, ok := idx.CallEdgeKinds[serviceID+"\x00"+chargeID]
	if !ok || edge.Kind != "interface_dispatch" || edge.Via != "example.com/ports/ports.Charger" {
		t.Fatalf("edge = %+v, ok=%v", edge, ok)
	}

	graph := idx.CallGraph(chargeID, "callers", 2, 80)
	if len(graph.Edges) == 0 {
		t.Fatalf("empty call graph: %+v", graph)
	}
	foundDispatch := false
	for _, e := range graph.Edges {
		if e.From == serviceID && e.To == chargeID {
			foundDispatch = e.Kind == "interface_dispatch" && e.Via == "example.com/ports/ports.Charger"
		}
	}
	if !foundDispatch {
		t.Fatalf("dispatch edge not labeled: %+v", graph.Edges)
	}

	filtered := idx.CallGraphWithOptions(chargeID, "callers", 2, 80, false)
	for _, e := range filtered.Edges {
		if e.From == serviceID && e.To == chargeID {
			t.Fatalf("dispatch edge present when disabled: %+v", filtered.Edges)
		}
	}
}

func TestInterfaceDispatchImpact(t *testing.T) {
	idx := buildPortsFixture(t)
	iface := idx.Symbols["example.com/ports/ports.Charger"]
	if iface == nil {
		t.Fatal("missing interface symbol")
	}
	impact := idx.ImpactBySymbol(iface, 3)
	if !slices.ContainsFunc(impact.Implementations, func(s ImpactSymbol) bool {
		return s.ID == "example.com/ports/payments.Module" && !s.Test
	}) {
		t.Fatalf("production implementation missing: %+v", impact.Implementations)
	}
	if !slices.ContainsFunc(impact.TestMocks, func(s ImpactSymbol) bool {
		return s.ID == "example.com/ports/checkout.mockCharger" && s.Test
	}) {
		t.Fatalf("test mock missing: %+v", impact.TestMocks)
	}
	if !slices.Contains(impact.TestPackagesToRun, "./checkout") {
		t.Fatalf("test packages = %v", impact.TestPackagesToRun)
	}

	serviceID := "example.com/ports/checkout.Service.Pay"
	charge := idx.Symbols["example.com/ports/payments.Module.Charge"]
	chargeImpact := idx.ImpactBySymbol(charge, 3)
	if !slices.Contains(chargeImpact.TransitiveCallers, serviceID) {
		t.Fatalf("impact missed interface caller: %+v", chargeImpact.TransitiveCallers)
	}
	if !slices.Contains(chargeImpact.TestPackagesToRun, "./checkout") {
		t.Fatalf("charge test packages = %v", chargeImpact.TestPackagesToRun)
	}
	if idx.TestCovered[charge.ID] {
		t.Fatal("mock-only test must not mark the production adapter as covered")
	}
}

func TestInterfaceDispatchCoverageWithRealImplementation(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/realcoverage", map[string]string{
		"ports/ports.go": `package ports

type Charger interface{ Charge(int) error }
`,
		"payments/payments.go": `package payments

type Module struct{}

func (m *Module) Charge(int) error { return nil }
`,
		"checkout/checkout.go": `package checkout

import "example.com/realcoverage/ports"

func Pay(port ports.Charger, amount int) error { return port.Charge(amount) }
`,
		"checkout/checkout_test.go": `package checkout

import (
	"testing"

	"example.com/realcoverage/payments"
)

func TestPayReal(t *testing.T) {
	if err := Pay(payments.Module{}, 1); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if !idx.TestCovered["example.com/realcoverage/payments.Module.Charge"] {
		t.Fatal("interface dispatch through a test-wired production implementation was not covered")
	}
}

func TestGenericAndEmbeddedInterfacesDoNotPanic(t *testing.T) {
	idx := buildPortsFixture(t)
	impls := idx.IfaceImpls["example.com/ports/ports.Charger"]
	if len(impls) == 0 {
		t.Fatalf("interface matrix empty: %+v", idx.IfaceImpls)
	}
	for _, match := range impls {
		if strings.Contains(match.TypeID, "Generic") {
			t.Fatalf("uninstantiated generic type recorded as an implementation: %+v", match)
		}
	}
	if idx.Symbols["example.com/ports/ports.Extended"] == nil {
		t.Fatal("embedded interface not indexed")
	}
	if idx.Symbols["example.com/ports/ports.Inline"] == nil {
		t.Fatal("inline embedded interface not indexed")
	}
}

func TestFieldAndStructImpact(t *testing.T) {
	idx := buildPortsFixture(t)
	order := idx.Symbols["example.com/ports/models.Order"]
	if order == nil {
		t.Fatal("missing Order")
	}
	if idx.Symbols["example.com/ports/models.Order.ID"] == nil {
		t.Fatal("struct field symbol missing")
	}

	field, err := idx.SingleSymbol("Order.ID")
	if err != nil {
		t.Fatalf("SingleSymbol(Order.ID): %v", err)
	}
	if field.ID != "example.com/ports/models.Order.ID" {
		t.Fatalf("field resolved to %q", field.ID)
	}
	impact := idx.ImpactBySymbol(field, 3)
	if len(impact.DirectReferences) == 0 {
		t.Fatalf("field has no references: %+v", field)
	}
	other := idx.Symbols["example.com/ports/models.Other.ID"]
	if other == nil {
		t.Fatal("missing Other.ID")
	}
	otherImpact := idx.ImpactBySymbol(other, 3)
	if len(otherImpact.DirectReferences) != 1 || otherImpact.DirectReferences[0].FromFuncID != "example.com/ports/models.UseOther" {
		t.Fatalf("Other.ID references = %+v", otherImpact.DirectReferences)
	}
	orderImpact := idx.ImpactBySymbol(order, 3)
	if len(orderImpact.CompositeLiterals) == 0 || len(orderImpact.UnkeyedLiterals) != 0 {
		t.Fatalf("order composite sites = %+v unkeyed=%+v", orderImpact.CompositeLiterals, orderImpact.UnkeyedLiterals)
	}

	sentinel := idx.Symbols["example.com/ports/models.ErrNotFound"]
	if sentinel == nil {
		t.Fatal("missing sentinel")
	}
	sentinelImpact := idx.ImpactBySymbol(sentinel, 3)
	if !slices.ContainsFunc(sentinelImpact.DirectReferences, func(ref ImpactRef) bool {
		return ref.FromFuncID == "example.com/ports/models.IsMissing"
	}) {
		t.Fatalf("sentinel references = %+v", sentinelImpact.DirectReferences)
	}
}

func TestInterfaceDispatchSkipsStdlibHooks(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/stdlib", map[string]string{
		"a/a.go": `package a

import "fmt"

type Adapter struct{}

func (Adapter) String() string { return "a" }

func Use(a Adapter) string { return fmt.Sprint(a) }
`,
		"b/b.go": `package b

type Other struct{}

func (Other) String() string { return "b" }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for key, edge := range idx.CallEdgeKinds {
		if edge.Kind == "interface_dispatch" && edge.Via != "" {
			t.Fatalf("stdlib interface expanded: %s -> %+v", key, edge)
		}
	}
	// The stdlib interface must still suppress dead-code false positives.
	if !idx.IfaceMethods["example.com/stdlib/a.Adapter.String"] {
		t.Fatal("fmt.Stringer satisfaction not recorded")
	}
}

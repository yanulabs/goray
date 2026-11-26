package index

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGenericFieldReferencesUseOrigins(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/fields", map[string]string{
		"model/model.go": `package model

type Box[T any] struct {
	Value T
	Count int
}

func ReadInt(b Box[int]) int { return b.Value }
func ReadGeneric[T any](b Box[T]) T { return b.Value }
`,
		"use/use.go": `package use

import "example.com/fields/model"

func ReadString(b model.Box[string]) string { return b.Value }

func Build() model.Box[int] {
	return model.Box[int]{Value: 1, Count: 2}
}
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		field      string
		wantFrom   []string
		exactCount int
	}{
		{
			field:    "example.com/fields/model.Box.Value",
			wantFrom: []string{"example.com/fields/model.ReadInt", "example.com/fields/model.ReadGeneric", "example.com/fields/use.ReadString", "example.com/fields/use.Build"},
		},
		{
			field:    "example.com/fields/model.Box.Count",
			wantFrom: []string{"example.com/fields/use.Build"},
		},
	}
	for _, tc := range cases {
		if idx.Symbols[tc.field] == nil {
			t.Fatalf("missing field symbol %s", tc.field)
		}
		refs := idx.XRefs[tc.field]
		for _, want := range tc.wantFrom {
			if !slices.ContainsFunc(refs, func(ref XRef) bool { return ref.FromFuncID == want }) {
				t.Fatalf("%s missing reference from %s: %+v", tc.field, want, refs)
			}
		}
	}
}

func TestWorkspaceCrossModuleFieldAndInterfaceIdentity(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, filepath.Join(root, "modulea"), "example.com/a", map[string]string{
		"a.go": `package a

import "example.com/b"

type Impl struct{}

func (Impl) Read() b.Doc { return b.Doc{} }
func Use(o b.Outer) int  { return o.Number }
func Make() b.Box        { return b.Box{Value: 1} }

var _ b.Reader = Impl{}
`,
	})
	writeTagModule(t, filepath.Join(root, "moduleb"), "example.com/b", map[string]string{
		"b.go": `package b

type Doc struct{ Title string }

type Box struct{ Value int }

type Inner struct{ Number int }
type Outer struct{ Inner }

type Reader interface{ Read() Doc }
`,
	})
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.23\n\nuse (\n\t./modulea\n\t./moduleb\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := BuildWithOptions(root, BuildOptions{Workspace: true})
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{
		"example.com/b.Box.Value":    "example.com/a.Make",
		"example.com/b.Inner.Number": "example.com/a.Use",
	} {
		if !slices.ContainsFunc(idx.XRefs[field], func(ref XRef) bool { return ref.FromFuncID == want }) {
			t.Fatalf("%s missing cross-module reference from %s: %+v", field, want, idx.XRefs[field])
		}
	}

	matches := idx.IfaceImpls["example.com/b.Reader"]
	if !slices.ContainsFunc(matches, func(match ImplMatch) bool { return match.TypeID == "example.com/a.Impl" }) {
		t.Fatalf("cross-module implementation matrix missing Impl: %+v", matches)
	}
	if !slices.ContainsFunc(idx.TypeImpls["example.com/a.Impl"], func(match ImplMatch) bool {
		return match.IfaceID == "example.com/b.Reader"
	}) {
		t.Fatalf("cross-module type implementation missing Reader: %+v", idx.TypeImpls["example.com/a.Impl"])
	}
}

func TestIncrementalFieldReferencesSurviveExportData(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/incremental", map[string]string{
		"model/model.go": `package model

type Box struct{ Number int }
`,
		"use/use.go": `package use

import "example.com/incremental/model"

func Read(b model.Box) int { return b.Number }
`,
		"other/other.go": `package other

func Value() int { return 1 }
`,
	})
	prev, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	useFile := filepath.Join(root, "use", "use.go")
	if err := os.WriteFile(useFile, []byte(`package use

import "example.com/incremental/model"

func Read(b model.Box) int { return b.Number + 1 }

func Build() model.Box { return model.Box{Number: 2} }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	next, used, _, err := BuildIncremental(root, BuildOptions{}, prev, []string{useFile})
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("expected an incremental rebuild")
	}
	refs := next.XRefs["example.com/incremental/model.Box.Number"]
	for _, want := range []string{"example.com/incremental/use.Read", "example.com/incremental/use.Build"} {
		if !slices.ContainsFunc(refs, func(ref XRef) bool { return ref.FromFuncID == want }) {
			t.Fatalf("incremental field refs missing %s: %+v", want, refs)
		}
	}

	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, full, next)
}

func TestPromotedMethodInterfaceDispatchAndImpact(t *testing.T) {
	root := t.TempDir()
	writeTagModule(t, root, "example.com/promoted", map[string]string{
		"ports/ports.go": `package ports

type Runner interface{ Run() int }
`,
		"lib/lib.go": `package lib

type Base struct{}

func (Base) Run() int    { return 1 }
func (Base) Unrelated()  {}
`,
		"app/app.go": `package app

import (
	"example.com/promoted/lib"
	"example.com/promoted/ports"
)

type Wrapper struct{ lib.Base }

func (Wrapper) Other() {}

func Use(r ports.Runner) int { return r.Run() }
func Elsewhere()             { Wrapper{}.Other() }
`,
	})
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	matches := idx.IfaceImpls["example.com/promoted/ports.Runner"]
	var wrapper *ImplMatch
	for i := range matches {
		if matches[i].TypeID == "example.com/promoted/app.Wrapper" {
			wrapper = &matches[i]
		}
	}
	if wrapper == nil {
		t.Fatalf("Wrapper missing from interface matrix: %+v", matches)
	}
	if got := wrapper.MethodIDs["Run"]; got != "example.com/promoted/lib.Base.Run" {
		t.Fatalf("promoted method mapping = %q, want lib.Base.Run", got)
	}

	useID := "example.com/promoted/app.Use"
	runID := "example.com/promoted/lib.Base.Run"
	if !slices.Contains(idx.Callers[runID], useID) {
		t.Fatalf("promoted dispatch caller missing: %+v", idx.Callers[runID])
	}
	edge := idx.CallEdgeKinds[useID+"\x00"+runID]
	if edge.Kind != "interface_dispatch" || edge.Via != "example.com/promoted/ports.Runner" {
		t.Fatalf("promoted dispatch edge = %+v", edge)
	}

	iface := idx.Symbols["example.com/promoted/ports.Runner"]
	impact := idx.ImpactBySymbol(iface, 3)
	if !slices.Contains(impact.TransitiveCallers, useID) {
		t.Fatalf("interface impact missing promoted dispatch caller: %+v", impact.TransitiveCallers)
	}
	if slices.Contains(impact.TransitiveCallers, "example.com/promoted/app.Elsewhere") {
		t.Fatalf("interface impact pulled in an unrelated method caller: %+v", impact.TransitiveCallers)
	}
}

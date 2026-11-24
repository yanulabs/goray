package index

import (
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestCanonicalAnonymousCompositeTypes(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

import "io"

type S struct {
	Closer interface {
		Close() error
	}
	Reader interface {
		io.Reader
		Reset()
	}
	Duplicate interface {
		Close() error
	}
}

func Use(in interface{ Read([]byte) (int, error) }) interface{ Close() error } {
	return nil
}

func (s S) Accept(in interface{ Close() error }) interface{ Read([]byte) (int, error) } {
	return nil
}

type I = interface{ Close() error }

type A interface { B() B }
type B interface { A() A }

type Comparable[T Comparable[T]] interface {
	Less(T) bool
}

type Named struct { Next *Named }
type MyAlias = Named
type Defined Named
`}
	idx, dir := buildTestModule(t, files)
	ids := []string{
		"sym:example.com/sig/a.S",
		"sym:example.com/sig/a.(S).Accept",
		"sym:example.com/sig/a.Use",
		"sym:example.com/sig/a.I",
		"sym:example.com/sig/a.A",
		"sym:example.com/sig/a.B",
		"sym:example.com/sig/a.Comparable",
		"sym:example.com/sig/a.Named",
		"sym:example.com/sig/a.MyAlias",
		"sym:example.com/sig/a.Defined",
	}
	before := map[string]string{}
	for _, id := range ids {
		sym := summaryOf(t, idx, id)
		if sym.ContentHash == "" {
			t.Fatalf("%s has empty content hash: %+v", id, sym)
		}
		before[id] = sym.ContentHash
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

	pkgs, err := packages.Load(&packages.Config{
		Dir:  dir,
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedFiles | packages.NeedImports | packages.NeedDeps,
	}, "./...")
	if err != nil || len(pkgs) != 1 || pkgs[0].Types == nil {
		t.Fatalf("load types: %v (%d packages)", err, len(pkgs))
	}
	scope := pkgs[0].Types.Scope()
	sType, ok := scope.Lookup("S").Type().Underlying().(*types.Struct)
	if !ok {
		t.Fatal("S is not a struct")
	}
	b := &builder{modPath: "example.com/sig"}
	closer := b.canonicalType(sType.Field(0).Type())
	reader := b.canonicalType(sType.Field(1).Type())
	duplicate := b.canonicalType(sType.Field(2).Type())
	if closer != duplicate {
		t.Fatalf("equal anonymous interfaces differ:\n%s\n%s", closer, duplicate)
	}
	if !strings.Contains(closer, "interface{Close") || strings.Contains(closer, "recv(") || strings.Contains(closer, "cycle(") {
		t.Fatalf("anonymous interface canonical form = %q", closer)
	}
	if !strings.Contains(reader, "interface{") || !strings.Contains(reader, "Reset") || strings.Contains(reader, "recv(") {
		t.Fatalf("embedded anonymous interface canonical form = %q", reader)
	}
	if got := b.canonicalType(sType.Field(0).Type()); got != closer {
		t.Fatalf("canonical form is not repeatable: %q != %q", got, closer)
	}

	use, ok := scope.Lookup("Use").Type().(*types.Signature)
	if !ok {
		t.Fatal("Use is not a function")
	}
	useForm := b.canonicalType(use)
	if !strings.Contains(useForm, "interface{Read") || !strings.Contains(useForm, "interface{Close") {
		t.Fatalf("function anonymous interfaces = %q", useForm)
	}

	for _, name := range []string{"A", "B", "Comparable", "I"} {
		form := b.canonicalType(scope.Lookup(name).Type())
		if form == "" || len(form) > 4096 {
			t.Fatalf("%s canonical form suspicious: %d bytes", name, len(form))
		}
		if strings.Contains(form, "recv(") {
			t.Fatalf("%s serialized a synthetic receiver: %q", name, form)
		}
	}
	aForm := b.canonicalType(scope.Lookup("A").Type())
	if !strings.Contains(aForm, "cycle(") {
		t.Fatalf("mutually recursive interface lacks cycle marker: %q", aForm)
	}
	comparable := b.canonicalType(scope.Lookup("Comparable").Type())
	if !strings.Contains(comparable, "cycle(") {
		t.Fatalf("recursive generic constraint lacks cycle marker: %q", comparable)
	}
	if alias := b.canonicalType(scope.Lookup("MyAlias").Type()); alias == "" {
		t.Fatal("alias canonical form is empty")
	}

	for _, typ := range []types.Type{use, sType, scope.Lookup("A").Type(), scope.Lookup("Comparable").Type()} {
		const printed = "func(int) error"
		want := hashText(printed + "\n" + b.canonicalType(typ))
		if got := b.canonicalSignatureHash(printed, typ); got != want {
			t.Fatalf("streamed signature hash changed: got %s want %s", got, want)
		}
	}
}

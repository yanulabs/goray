package index

import (
	"slices"
	"strings"
	"testing"
)

func buildTypeDetailFixture(t *testing.T, opts BuildOptions) *Index {
	t.Helper()
	root := t.TempDir()
	writeTagModule(t, root, "example.com/details", map[string]string{
		"domain/domain.go": `package domain

// Base is embedded by Embedded.
type Base struct {
	ID     int ` + "`json:\"id\"`" + `
	hidden string
}

// ValueMethod is promoted through Embedded.
func (b Base) ValueMethod() int { return b.ID }

// PointerMethod is pointer-only.
func (b *Base) PointerMethod() {}

// Embedded adds a note.
type Embedded struct {
	Base
	Note string
}

// PointerEmbedded is pointer-embedded by Service.
type PointerEmbedded struct {
	Port int ` + "`json:\"port\"`" + `
}

// Service exercises declared, embedded, and promoted members.
type Service struct {
	Embedded
	*PointerEmbedded
	Name    string ` + "`json:\"name\"`" + `
	private bool
}

func (s Service) ServiceMethod() string { return s.Name }

// Reader is embedded by ReadCloser.
type Reader interface {
	Read(p []byte) (int, error)
}

// Closer is embedded by ReadCloser.
type Closer interface {
	Close() error
}

// ReadCloser composes Reader and Closer.
type ReadCloser interface {
	Reader
	Closer
	Name() string
}

// Number is a type constraint, not a value interface.
type Number interface {
	~int | ~int64
}

// Box is generic.
type Box[T any] struct {
	Value T
	Count int
}

func (b Box[T]) Get() T      { return b.Value }
func (b *Box[T]) Set(value T) { b.Value = value }

// IntBox is an alias of Box[int].
type IntBox = Box[int]

type File struct{}

func (File) Read(p []byte) (int, error) { return 0, nil }
func (File) Close() error               { return nil }
func (File) Name() string                { return "file" }

func NewService() *Service                  { return &Service{} }
func NewServiceValue() Service              { return Service{} }
func NewServiceWithError() (*Service, error) { return &Service{}, nil }
func NewServiceWithBool() (Service, bool)    { return Service{}, false }
func newService() *Service                   { return &Service{} }

func NewBoundBox(v int) (IntBox, error) { return IntBox{Value: v}, nil }
func NewBox[T any](value T) *Box[T]     { return &Box[T]{Value: value} }
func MakeBox[T ~int](value T) (Box[T], error) {
	return Box[T]{Value: value}, nil
}

func Use(s Service) string { return s.Name }
`,
		"domain/domain_test.go": `package domain

type mockReadCloser struct{}

func (*mockReadCloser) Read(p []byte) (int, error) { return 0, nil }
func (*mockReadCloser) Close() error               { return nil }
func (*mockReadCloser) Name() string                { return "mock" }
`,
	})
	idx, err := BuildWithOptions(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func findField(t *testing.T, fields []FieldDetail, name string) FieldDetail {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("field %q not found in %+v", name, fields)
	return FieldDetail{}
}

func findMethod(t *testing.T, methods []MethodDetail, name string) MethodDetail {
	t.Helper()
	for _, method := range methods {
		if method.Name == name {
			return method
		}
	}
	t.Fatalf("method %q not found in %+v", name, methods)
	return MethodDetail{}
}

func findInterfaceMethod(t *testing.T, methods []InterfaceMethodDetail, name string) InterfaceMethodDetail {
	t.Helper()
	for _, method := range methods {
		if method.Name == name {
			return method
		}
	}
	t.Fatalf("interface method %q not found in %+v", name, methods)
	return InterfaceMethodDetail{}
}

func TestTypeDetailsBuild(t *testing.T) {
	const pkg = "example.com/details/domain"
	tests := []struct {
		name string
		opts BuildOptions
	}{
		{name: "retained"},
		{name: "released", opts: BuildOptions{ReleaseArtifacts: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			idx := buildTypeDetailFixture(t, test.opts)
			if test.opts.ReleaseArtifacts && (idx.pkgs != nil || idx.testPkgs != nil) {
				t.Fatal("released index retained package artifacts")
			}
			assertServiceDetail(t, idx, pkg)
			assertInterfaceDetails(t, idx, pkg)
			assertGenericDetails(t, idx, pkg)
			assertAliasDetails(t, idx, pkg)
			assertImplementationDetails(t, idx, pkg)
		})
	}
}

func assertServiceDetail(t *testing.T, idx *Index, pkg string) {
	t.Helper()
	service := idx.TypeDetails[pkg+".Service"]
	if service.Kind != "struct" || service.File != "domain/domain.go" || service.Line == 0 {
		t.Fatalf("service declaration = %+v", service)
	}
	if !strings.Contains(service.Doc, "Service exercises") {
		t.Fatalf("service doc = %q", service.Doc)
	}
	if service.FieldCount != 4 {
		t.Fatalf("service field count = %d, want 4", service.FieldCount)
	}

	name := findField(t, service.Fields, "Name")
	if name.Tag != `json:"name"` || !name.Exported || name.Embedded || name.PromotedFrom != "" {
		t.Fatalf("declared Name = %+v", name)
	}
	if name.ID != pkg+".Service.Name" || name.XRefCount == 0 {
		t.Fatalf("Name ID/xrefs = %+v", name)
	}
	if findField(t, service.Fields, "private").Exported {
		t.Fatal("private field is exported")
	}
	embedded := findField(t, service.Fields, "Embedded")
	if !embedded.Embedded || embedded.PromotedFrom != "" {
		t.Fatalf("embedded field = %+v", embedded)
	}

	id := findField(t, service.Fields, "ID")
	if id.PromotedFrom != "Embedded.Base" || id.Tag != `json:"id"` || id.ID != pkg+".Base.ID" {
		t.Fatalf("promoted ID = %+v", id)
	}
	port := findField(t, service.Fields, "Port")
	if port.PromotedFrom != "*PointerEmbedded" || port.Tag != `json:"port"` {
		t.Fatalf("promoted Port = %+v", port)
	}

	valueMethod := findMethod(t, service.MethodSet.Value, "ValueMethod")
	if valueMethod.PromotedFrom != "Embedded.Base" || valueMethod.RecvPtr {
		t.Fatalf("promoted value method = %+v", valueMethod)
	}
	findMethod(t, service.MethodSet.Value, "ServiceMethod")
	pointerMethod := findMethod(t, service.MethodSet.Pointer, "PointerMethod")
	if pointerMethod.PromotedFrom != "Embedded.Base" || !pointerMethod.RecvPtr {
		t.Fatalf("promoted pointer method = %+v", pointerMethod)
	}
	if slices.ContainsFunc(service.MethodSet.Value, func(m MethodDetail) bool { return m.Name == "PointerMethod" }) {
		t.Fatal("pointer-only method leaked into the value set")
	}
}

func assertInterfaceDetails(t *testing.T, idx *Index, pkg string) {
	t.Helper()
	readCloser := idx.TypeDetails[pkg+".ReadCloser"]
	if readCloser.Kind != "interface" {
		t.Fatalf("ReadCloser kind = %q", readCloser.Kind)
	}
	read := findInterfaceMethod(t, readCloser.Methods, "Read")
	closeMethod := findInterfaceMethod(t, readCloser.Methods, "Close")
	nameMethod := findInterfaceMethod(t, readCloser.Methods, "Name")
	if read.EmbeddedFrom != "Reader" || closeMethod.EmbeddedFrom != "Closer" || nameMethod.EmbeddedFrom != "" {
		t.Fatalf("interface methods = %+v", readCloser.Methods)
	}
	if len(readCloser.EmbeddedInterfaces) != 2 ||
		readCloser.EmbeddedInterfaces[0].ID != pkg+".Reader" ||
		readCloser.EmbeddedInterfaces[1].ID != pkg+".Closer" {
		t.Fatalf("embedded interfaces = %+v", readCloser.EmbeddedInterfaces)
	}
	if !slices.ContainsFunc(readCloser.MethodSet.Value, func(m MethodDetail) bool {
		return m.Name == "Read" && m.PromotedFrom == "Reader"
	}) {
		t.Fatalf("interface method set = %+v", readCloser.MethodSet.Value)
	}

	number := idx.TypeDetails[pkg+".Number"]
	if len(number.EmbeddedInterfaces) != 1 || number.EmbeddedInterfaces[0].Kind != "constraint" {
		t.Fatalf("Number constraints = %+v", number.EmbeddedInterfaces)
	}
}

func assertGenericDetails(t *testing.T, idx *Index, pkg string) {
	t.Helper()
	box := idx.TypeDetails[pkg+".Box"]
	if len(box.TypeParams) != 1 || box.TypeParams[0].Name != "T" || box.TypeParams[0].Constraint != "any" {
		t.Fatalf("Box type params = %+v", box.TypeParams)
	}
	if value := findField(t, box.Fields, "Value"); value.Type != "T" {
		t.Fatalf("Box.Value type = %q", value.Type)
	}
	if len(box.Constructors) != 3 {
		t.Fatalf("Box constructors = %+v", box.Constructors)
	}
	if !slices.ContainsFunc(box.Constructors, func(c ConstructorDetail) bool {
		return c.Name == "MakeBox" && strings.Contains(c.Signature, "error") && c.Returns == "Box[T]"
	}) {
		t.Fatalf("MakeBox constructor = %+v", box.Constructors)
	}
	if got := findMethod(t, box.MethodSet.Value, "Get"); got.ID != pkg+".Box.Get" {
		t.Fatalf("Box value method = %+v", got)
	}
	if got := findMethod(t, box.MethodSet.Pointer, "Set"); !got.RecvPtr || got.ID != pkg+".Box.Set" {
		t.Fatalf("Box pointer method = %+v", got)
	}
}

func assertAliasDetails(t *testing.T, idx *Index, pkg string) {
	t.Helper()
	alias := idx.TypeDetails[pkg+".IntBox"]
	if !alias.Alias || alias.Kind != "type" || alias.Underlying != "struct" || alias.AliasTarget != "Box[int]" {
		t.Fatalf("IntBox alias = %+v", alias)
	}
	if alias.AliasTargetID != pkg+".Box" {
		t.Fatalf("IntBox target ID = %q", alias.AliasTargetID)
	}
	if aliasValue := findField(t, alias.Fields, "Value"); aliasValue.ID != pkg+".Box.Value" {
		t.Fatalf("IntBox reuses target field ID: %+v", aliasValue)
	}
	if len(alias.Constructors) != 3 {
		t.Fatalf("IntBox constructors = %+v", alias.Constructors)
	}
	if got := findMethod(t, alias.MethodSet.Value, "Get"); got.ID != pkg+".Box.Get" {
		t.Fatalf("IntBox alias value method = %+v", got)
	}
	if got := findMethod(t, alias.MethodSet.Pointer, "Set"); !got.RecvPtr {
		t.Fatalf("IntBox alias pointer method = %+v", got)
	}
}

func assertImplementationDetails(t *testing.T, idx *Index, pkg string) {
	t.Helper()
	implementations := idx.IfaceImpls[pkg+".ReadCloser"]
	if !slices.ContainsFunc(implementations, func(m ImplMatch) bool {
		return m.TypeID == pkg+".File" && !idx.Symbols[m.TypeID].IsTest
	}) {
		t.Fatalf("production implementation missing: %+v", implementations)
	}
	if !slices.ContainsFunc(implementations, func(m ImplMatch) bool {
		return m.TypeID == pkg+".mockReadCloser" && idx.Symbols[m.TypeID].IsTest && len(m.MethodIDs) == 3
	}) {
		t.Fatalf("test mock missing: %+v", implementations)
	}
}

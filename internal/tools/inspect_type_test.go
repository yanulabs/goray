package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func buildInspectTypeFixture(t *testing.T) *index.Index {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/inspect\n\ngo 1.23\n",
		"domain/domain.go": `package domain

// Base is embedded.
type Base struct {
	ID int ` + "`json:\"id\"`" + `
}

func (b Base) ValueMethod() int { return b.ID }
func (b *Base) PointerMethod()   {}

// Embedded adds a note.
type Embedded struct {
	Base
	Note string
}

// PointerEmbedded is pointer-embedded.
type PointerEmbedded struct {
	Port int ` + "`json:\"port\"`" + `
}

// Service has declared, embedded, and promoted members.
type Service struct {
	Embedded
	*PointerEmbedded
	Label   string ` + "`json:\"label\"`" + `
	private bool
}

func (s Service) ServiceMethod() string { return s.Label }

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

// Box is generic.
type Box[T any] struct {
	Value T
	Count int
}

// IntBox aliases Box[int].
type IntBox = Box[int]

type File struct{}

func (File) Read(p []byte) (int, error) { return 0, nil }
func (File) Close() error               { return nil }
func (File) Name() string                { return "file" }

func NewService() *Service                    { return &Service{} }
func NewServiceValue() Service                { return Service{} }
func NewServiceWithError() (*Service, error)  { return &Service{}, nil }
func NewServiceWithBool() (Service, bool)     { return Service{}, false }
func newService() *Service                    { return &Service{} }
func NewBoundBox(v int) (IntBox, error)       { return IntBox{Value: v}, nil }
func NewBox[T any](value T) *Box[T]           { return &Box[T]{Value: value} }
func MakeBox[T ~int](value T) (Box[T], error) { return Box[T]{Value: value}, nil }
`,
		"domain/domain_test.go": `package domain

type mockReadCloser struct{}

func (*mockReadCloser) Read(p []byte) (int, error) { return 0, nil }
func (*mockReadCloser) Close() error               { return nil }
func (*mockReadCloser) Name() string                { return "mock" }
`,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.BuildWithOptions(root, index.BuildOptions{ReleaseArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

type inspectTypeOutput struct {
	ID                 string                        `json:"id"`
	CanonicalID        string                        `json:"canonical_id"`
	Kind               string                        `json:"kind"`
	Alias              bool                          `json:"alias"`
	AliasTarget        string                        `json:"alias_target"`
	Underlying         string                        `json:"underlying"`
	Doc                string                        `json:"doc"`
	TypeParams         []index.TypeParamDetail       `json:"type_params"`
	Fields             []index.FieldDetail           `json:"fields"`
	Methods            []index.InterfaceMethodDetail `json:"methods"`
	EmbeddedInterfaces []index.EmbeddedInterface     `json:"embedded_interfaces"`
	MethodSet          index.MethodSetDetail         `json:"method_set"`
	Constructors       []index.ConstructorDetail     `json:"constructors"`
	Implementations    []inspectImplementation       `json:"implementations"`
	TestMocks          []inspectImplementation       `json:"test_mocks"`
	Implements         []inspectImplementation       `json:"implements"`
	Summary            json.RawMessage               `json:"summary"`
	SyncState          string                        `json:"sync_state"`
	IndexState         index.State                   `json:"index_state"`
}

func runInspectType(t *testing.T, idx *index.Index, args map[string]any) inspectTypeOutput {
	t.Helper()
	text, failed := projectionTool(t, idx, "inspect_type", nil).Run(context.Background(), args)
	if failed {
		t.Fatalf("inspect_type failed: %s", text)
	}
	var out inspectTypeOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return out
}

func fieldByName(t *testing.T, fields []index.FieldDetail, name string) index.FieldDetail {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("field %q not found in %+v", name, fields)
	return index.FieldDetail{}
}

func methodByName(t *testing.T, methods []index.MethodDetail, name string) index.MethodDetail {
	t.Helper()
	for _, method := range methods {
		if method.Name == name {
			return method
		}
	}
	t.Fatalf("method %q not found in %+v", name, methods)
	return index.MethodDetail{}
}

func TestInspectTypeStructAndFilters(t *testing.T) {
	idx := buildInspectTypeFixture(t)
	const id = "example.com/inspect/domain.Service"

	out := runInspectType(t, idx, map[string]any{"symbol_id": id})
	if out.Kind != "struct" || out.ID != id || out.CanonicalID != "sym:"+id {
		t.Fatalf("declaration = %+v", out)
	}
	if out.IndexState != index.StateReady {
		t.Fatalf("index_state = %q", out.IndexState)
	}
	if !strings.Contains(out.Doc, "declared, embedded") {
		t.Fatalf("doc = %q", out.Doc)
	}
	label := fieldByName(t, out.Fields, "Label")
	if label.Tag != `json:"label"` || label.ID != id+".Label" {
		t.Fatalf("Label = %+v", label)
	}
	if fieldByName(t, out.Fields, "private").Exported {
		t.Fatal("private field should be unexported")
	}
	idField := fieldByName(t, out.Fields, "ID")
	if idField.PromotedFrom != "Embedded.Base" || idField.Tag != `json:"id"` {
		t.Fatalf("promoted ID = %+v", idField)
	}
	port := fieldByName(t, out.Fields, "Port")
	if port.PromotedFrom != "*PointerEmbedded" || port.Tag != `json:"port"` {
		t.Fatalf("promoted Port = %+v", port)
	}
	if got := methodByName(t, out.MethodSet.Value, "ValueMethod"); got.PromotedFrom != "Embedded.Base" {
		t.Fatalf("promoted value method = %+v", got)
	}
	if got := methodByName(t, out.MethodSet.Pointer, "PointerMethod"); !got.RecvPtr || got.PromotedFrom != "Embedded.Base" {
		t.Fatalf("promoted pointer method = %+v", got)
	}
	if len(out.Constructors) != 4 {
		t.Fatalf("constructors = %+v", out.Constructors)
	}
	if !hasConstructor(out.Constructors, "NewServiceWithError") || hasConstructor(out.Constructors, "NewServiceWithBool") {
		t.Fatalf("constructor filtering = %+v", out.Constructors)
	}

	filtered := runInspectType(t, idx, map[string]any{
		"symbol_id": id, "include_unexported": false, "include_promoted": false,
	})
	if len(filtered.Fields) != 3 {
		t.Fatalf("filtered fields = %+v", filtered.Fields)
	}
	if hasField(filtered.Fields, "private") || hasField(filtered.Fields, "ID") || hasField(filtered.Fields, "Port") {
		t.Fatalf("filters leaked fields: %+v", filtered.Fields)
	}
	if len(filtered.MethodSet.Value) != 1 || filtered.MethodSet.Value[0].Name != "ServiceMethod" {
		t.Fatalf("filtered value methods = %+v", filtered.MethodSet.Value)
	}
	if len(filtered.MethodSet.Pointer) != 0 {
		t.Fatalf("filtered pointer methods = %+v", filtered.MethodSet.Pointer)
	}
	if hasConstructor(filtered.Constructors, "newService") || len(filtered.Constructors) != 3 {
		t.Fatalf("filtered constructors = %+v", filtered.Constructors)
	}
}

func TestInspectTypeInterfaceImplementations(t *testing.T) {
	idx := buildInspectTypeFixture(t)
	const id = "example.com/inspect/domain.ReadCloser"

	out := runInspectType(t, idx, map[string]any{"symbol_id": "ReadCloser"})
	if out.Kind != "interface" || out.ID != id {
		t.Fatalf("interface = %+v", out)
	}
	if len(out.Methods) != 3 {
		t.Fatalf("methods = %+v", out.Methods)
	}
	method := methodByName(t, out.MethodSet.Value, "Read")
	if method.PromotedFrom != "Reader" {
		t.Fatalf("Read method set = %+v", method)
	}
	if len(out.EmbeddedInterfaces) != 2 || out.EmbeddedInterfaces[0].ID != "example.com/inspect/domain.Reader" {
		t.Fatalf("embedded interfaces = %+v", out.EmbeddedInterfaces)
	}
	if len(out.Implementations) != 1 || out.Implementations[0].TypeID != "example.com/inspect/domain.File" {
		t.Fatalf("implementations = %+v", out.Implementations)
	}
	if len(out.Implementations[0].MethodIDs) != 3 {
		t.Fatalf("implementation method IDs = %+v", out.Implementations[0])
	}
	if len(out.TestMocks) != 1 || out.TestMocks[0].TypeID != "example.com/inspect/domain.mockReadCloser" || !out.TestMocks[0].Test {
		t.Fatalf("test mocks = %+v", out.TestMocks)
	}

	filtered := runInspectType(t, idx, map[string]any{
		"symbol_id": id, "include_unexported": false, "include_promoted": false,
	})
	if len(filtered.Methods) != 1 || filtered.Methods[0].Name != "Name" {
		t.Fatalf("filtered interface methods = %+v", filtered.Methods)
	}
	if len(filtered.TestMocks) != 0 {
		t.Fatalf("unexported test mock survived filter: %+v", filtered.TestMocks)
	}
	if len(filtered.Implementations) != 1 || filtered.Implementations[0].TypeID != "example.com/inspect/domain.File" {
		t.Fatalf("filtered implementations = %+v", filtered.Implementations)
	}
}

func TestInspectTypeGenericAliasAndConcreteImplements(t *testing.T) {
	idx := buildInspectTypeFixture(t)

	box := runInspectType(t, idx, map[string]any{"symbol_id": "example.com/inspect/domain.Box"})
	if len(box.TypeParams) != 1 || box.TypeParams[0].Name != "T" || box.TypeParams[0].Constraint != "any" {
		t.Fatalf("Box type params = %+v", box.TypeParams)
	}
	if fieldByName(t, box.Fields, "Value").Type != "T" {
		t.Fatalf("Box fields = %+v", box.Fields)
	}
	if len(box.Constructors) != 3 || !hasConstructor(box.Constructors, "MakeBox") {
		t.Fatalf("Box constructors = %+v", box.Constructors)
	}

	alias := runInspectType(t, idx, map[string]any{"symbol_id": "IntBox"})
	if !alias.Alias || alias.Kind != "type" || alias.AliasTarget != "Box[int]" || alias.Underlying != "struct" {
		t.Fatalf("alias = %+v", alias)
	}
	if fieldByName(t, alias.Fields, "Value").ID != "example.com/inspect/domain.Box.Value" {
		t.Fatalf("alias fields = %+v", alias.Fields)
	}
	if len(alias.Constructors) != 3 {
		t.Fatalf("alias constructors = %+v", alias.Constructors)
	}

	file := runInspectType(t, idx, map[string]any{"symbol_id": "File"})
	if len(file.Implements) == 0 {
		t.Fatal("File implements nothing")
	}
	if !hasImplementation(file.Implements, "example.com/inspect/domain.ReadCloser") {
		t.Fatalf("File implements = %+v", file.Implements)
	}
}

func TestInspectTypeErrorsAndSummary(t *testing.T) {
	idx := buildInspectTypeFixture(t)
	tool := projectionTool(t, idx, "inspect_type", nil)

	text, failed := tool.Run(context.Background(), map[string]any{"symbol_id": "example.com/inspect/domain.NewService"})
	if !failed || !strings.Contains(text, "requires a struct, interface, or named/alias type") {
		t.Fatalf("function target = %q failed=%v", text, failed)
	}
	text, failed = tool.Run(context.Background(), map[string]any{"symbol_id": "missing"})
	if !failed || !strings.Contains(text, "symbol_not_found") {
		t.Fatalf("missing target = %q failed=%v", text, failed)
	}

	summary := json.RawMessage(`{"intent":"service owns domain state"}`)
	summaries := &recordingSummaries{summaries: map[string]json.RawMessage{
		"sym:example.com/inspect/domain.Service": summary,
	}}
	out := struct {
		Summary    json.RawMessage `json:"summary"`
		SyncState  string          `json:"sync_state"`
		IndexState index.State     `json:"index_state"`
	}{}
	text, failed = projectionTool(t, idx, "inspect_type", summaries).Run(context.Background(), map[string]any{
		"symbol_id": "Service",
	})
	if failed {
		t.Fatal(text)
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Summary), "service owns domain state") || out.SyncState == "" {
		t.Fatalf("summary enrichment = %s state=%q", out.Summary, out.SyncState)
	}
	if len(summaries.enrich) != 1 || summaries.enrich[0] != "sym:example.com/inspect/domain.Service" {
		t.Fatalf("enrich calls = %v", summaries.enrich)
	}
}

func hasField(fields []index.FieldDetail, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func hasConstructor(constructors []index.ConstructorDetail, name string) bool {
	for _, constructor := range constructors {
		if constructor.Name == name {
			return true
		}
	}
	return false
}

func hasImplementation(implementations []inspectImplementation, id string) bool {
	for _, implementation := range implementations {
		if implementation.IfaceID == id {
			return true
		}
	}
	return false
}

func TestInspectTypeFilterMatrix(t *testing.T) {
	idx := buildInspectTypeFixture(t)
	const serviceID = "example.com/inspect/domain.Service"
	const interfaceID = "example.com/inspect/domain.ReadCloser"

	tests := []struct {
		name                   string
		symbolID               string
		args                   map[string]any
		wantPrivate            bool
		wantPromotedField      bool
		wantPromotedMethod     bool
		wantMockImplementation bool
	}{
		{
			name: "defaults include everything", symbolID: serviceID,
			args: map[string]any{}, wantPrivate: true, wantPromotedField: true, wantPromotedMethod: true,
		},
		{
			name: "exclude unexported", symbolID: serviceID,
			args: map[string]any{"include_unexported": false}, wantPromotedField: true, wantPromotedMethod: true,
		},
		{
			name: "exclude promoted", symbolID: serviceID,
			args: map[string]any{"include_promoted": false}, wantPrivate: true,
		},
		{
			name: "exclude both", symbolID: serviceID,
			args: map[string]any{"include_unexported": false, "include_promoted": false},
		},
		{
			name: "interface defaults", symbolID: interfaceID,
			args: map[string]any{}, wantMockImplementation: true,
		},
		{
			name: "interface exclude unexported mocks", symbolID: interfaceID,
			args: map[string]any{"include_unexported": false},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.args["symbol_id"] == nil {
				test.args["symbol_id"] = test.symbolID
			}
			out := runInspectType(t, idx, test.args)
			if got := hasField(out.Fields, "private"); got != test.wantPrivate {
				t.Fatalf("private field present = %v, want %v: %+v", got, test.wantPrivate, out.Fields)
			}
			if got := hasField(out.Fields, "ID"); got != test.wantPromotedField {
				t.Fatalf("promoted ID present = %v, want %v: %+v", got, test.wantPromotedField, out.Fields)
			}
			if got := hasMethod(out.MethodSet.Value, "ValueMethod"); got != test.wantPromotedMethod {
				t.Fatalf("promoted method present = %v, want %v: %+v", got, test.wantPromotedMethod, out.MethodSet.Value)
			}
			if got := len(out.TestMocks) > 0; got != test.wantMockImplementation {
				t.Fatalf("test mocks present = %v, want %v: %+v", got, test.wantMockImplementation, out.TestMocks)
			}
		})
	}
}

func hasMethod(methods []index.MethodDetail, name string) bool {
	for _, method := range methods {
		if method.Name == name {
			return true
		}
	}
	return false
}

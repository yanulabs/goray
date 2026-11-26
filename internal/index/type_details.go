package index

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// TypeDetail is compact, serializable metadata for one named type. It is
// computed while the type-checked package artifacts are still available so
// inspect_type never needs to retain go/types pointers after a build.
type TypeDetail struct {
	ID          string `json:"id"`
	CanonicalID string `json:"canonical_id,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // struct, interface, or type (aliases)
	PkgPath     string `json:"pkg_path"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	EndLine     int    `json:"end_line,omitempty"`
	Doc         string `json:"doc,omitempty"`

	Alias         bool   `json:"alias,omitempty"`
	AliasTarget   string `json:"alias_target,omitempty"`
	AliasTargetID string `json:"alias_target_id,omitempty"`

	Underlying string            `json:"underlying,omitempty"`
	TypeParams []TypeParamDetail `json:"type_params,omitempty"`

	FieldCount  int `json:"field_count,omitempty"`
	MethodCount int `json:"method_count,omitempty"`

	// Fields contains declared fields first, followed by recursively promoted
	// fields from embedded structs. PromotedFrom is empty for declared fields.
	Fields             []FieldDetail           `json:"fields,omitempty"`
	Methods            []InterfaceMethodDetail `json:"methods,omitempty"`
	EmbeddedInterfaces []EmbeddedInterface     `json:"embedded_interfaces,omitempty"`
	MethodSet          MethodSetDetail         `json:"method_set"`
	Constructors       []ConstructorDetail     `json:"constructors,omitempty"`
}

// TypeParamDetail is one type parameter and its constraint.
type TypeParamDetail struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
}

// FieldDetail is one struct field, declared or promoted.
type FieldDetail struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Tag          string `json:"tag,omitempty"`
	Exported     bool   `json:"exported"`
	Embedded     bool   `json:"embedded"`
	PromotedFrom string `json:"promoted_from"`
	XRefCount    int    `json:"xref_count"`
}

// InterfaceMethodDetail is one method in an interface's method set.
type InterfaceMethodDetail struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Signature    string `json:"signature"`
	EmbeddedFrom string `json:"embedded_from"`
	Exported     bool   `json:"exported"`
}

// EmbeddedInterface is an embedded interface or type constraint.
type EmbeddedInterface struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"`
	Kind     string `json:"kind"` // interface or constraint
	Exported bool   `json:"exported"`
}

// MethodDetail is one method in a value or pointer method set.
type MethodDetail struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Signature    string `json:"signature"`
	RecvPtr      bool   `json:"recv_ptr"`
	PromotedFrom string `json:"promoted_from"`
	Exported     bool   `json:"exported"`
}

// MethodSetDetail reports the value method set and the pointer-only methods
// that are not already present in the value set.
type MethodSetDetail struct {
	Value   []MethodDetail `json:"value"`
	Pointer []MethodDetail `json:"pointer"`
}

// ConstructorDetail is a package-level function whose first result is T or *T,
// optionally followed by an error.
type ConstructorDetail struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PkgPath   string `json:"pkg_path"`
	Signature string `json:"signature"`
	Returns   string `json:"returns"`
	Exported  bool   `json:"exported"`
}

func typeDoc(gen *ast.GenDecl, spec *ast.TypeSpec) string {
	parts := make([]string, 0, 2)
	if gen != nil && gen.Doc != nil {
		parts = append(parts, gen.Doc.Text())
	}
	if spec.Doc != nil {
		parts = append(parts, spec.Doc.Text())
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// addTypeDetail records serializable metadata for a type declaration. The
// declaration's own symbol remains the canonical identity; aliases reuse the
// target type's field and method symbol IDs rather than creating duplicates.
func (b *builder) addTypeDetail(p *packages.Package, gen *ast.GenDecl, spec *ast.TypeSpec, tn *types.TypeName, sym *Symbol) {
	q := qualifier(p.PkgPath)
	t := tn.Type()
	_, isAlias := t.(*types.Alias)

	detail := TypeDetail{
		ID: sym.ID, CanonicalID: sym.CanonicalID(), Name: sym.Name, Kind: sym.Kind,
		PkgPath: sym.PkgPath, File: sym.File, Line: sym.Line, EndLine: sym.EndLine,
		Doc: typeDoc(gen, spec), Alias: isAlias,
	}
	if isAlias {
		detail.Kind = "type"
		detail.AliasTarget = types.TypeString(types.Unalias(t), q)
		detail.AliasTargetID = detailNamedTypeID(types.Unalias(t))
	}

	isInterface := false
	switch u := t.Underlying().(type) {
	case *types.Struct:
		if !isAlias {
			detail.Kind = "struct"
		}
		detail.Underlying = "struct"
		detail.FieldCount = u.NumFields()
		detail.Fields = b.structFieldDetails(t, sym.ID, q)
	case *types.Interface:
		if !isAlias {
			detail.Kind = "interface"
		}
		isInterface = true
		detail.Underlying = "interface"
		detail.Methods, detail.EmbeddedInterfaces = interfaceMethodDetails(u, q)
	default:
		detail.Underlying = types.TypeString(u, q)
	}
	detail.TypeParams = typeParamDetails(t, q)
	if isInterface {
		detail.MethodSet = interfaceMethodSet(detail.Methods)
	} else {
		detail.MethodSet = typeMethodSet(t, q)
	}
	detail.MethodCount = len(detail.MethodSet.Value) + len(detail.MethodSet.Pointer)
	b.idx.TypeDetails[sym.ID] = detail
}

func typeParamDetails(t types.Type, q types.Qualifier) []TypeParamDetail {
	var params *types.TypeParamList
	switch x := t.(type) {
	case *types.Named:
		params = x.TypeParams()
	case *types.Alias:
		params = x.TypeParams()
	}
	if params == nil || params.Len() == 0 {
		return nil
	}
	out := make([]TypeParamDetail, 0, params.Len())
	for i := 0; i < params.Len(); i++ {
		param := params.At(i)
		out = append(out, TypeParamDetail{
			Name:       param.Obj().Name(),
			Constraint: types.TypeString(param.Constraint(), q),
		})
	}
	return out
}

func (b *builder) structFieldDetails(root types.Type, typeID string, q types.Qualifier) []FieldDetail {
	lookupRoot := types.Unalias(root)
	st := detailStructUnderlying(lookupRoot)
	if st == nil {
		return nil
	}
	ownerID := detailNamedTypeID(lookupRoot)
	if ownerID == "" {
		ownerID = typeID
	}

	fields := make([]FieldDetail, 0, st.NumFields())
	direct := make(map[string]bool, st.NumFields())
	for i := 0; i < st.NumFields(); i++ {
		field := st.Field(i)
		if field == nil {
			continue
		}
		direct[field.Name()] = true
		fields = append(fields, FieldDetail{
			ID:       fieldSymbolID(ownerID, field.Name()),
			Name:     field.Name(),
			Type:     types.TypeString(field.Type(), q),
			Tag:      st.Tag(i),
			Exported: field.Exported(),
			Embedded: field.Embedded(),
		})
	}

	names := map[string]bool{}
	collectNestedFieldNames(lookupRoot, map[types.Type]bool{}, names)
	nested := make([]string, 0, len(names))
	for name := range names {
		if !direct[name] {
			nested = append(nested, name)
		}
	}
	sort.Strings(nested)

	pkg := detailNamedTypePackage(lookupRoot, root)
	for _, name := range nested {
		obj, index, ok := lookupFieldOrMethod(lookupRoot, pkg, name)
		if !ok || len(index) < 2 {
			continue
		}
		field, ok := obj.(*types.Var)
		if !ok {
			continue
		}
		structField, tag, ok := structFieldAt(lookupRoot, index)
		if !ok || structField == nil {
			continue
		}
		fieldOwner := fieldOwnerID(lookupRoot, index)
		if fieldOwner == "" {
			fieldOwner = typeID
		}
		fields = append(fields, FieldDetail{
			ID:           fieldSymbolID(fieldOwner, field.Name()),
			Name:         field.Name(),
			Type:         types.TypeString(field.Type(), q),
			Tag:          tag,
			Exported:     field.Exported(),
			Embedded:     structField.Embedded(),
			PromotedFrom: promotedPath(lookupRoot, index, q),
		})
	}
	return fields
}

func fieldSymbolID(ownerID, name string) string {
	if ownerID == "" {
		return ""
	}
	return ownerID + "." + name
}

func collectNestedFieldNames(t types.Type, seen map[types.Type]bool, names map[string]bool) {
	t = detailDerefAlias(t)
	if t == nil || seen[t] {
		return
	}
	seen[t] = true
	st := detailStructUnderlying(t)
	if st == nil {
		return
	}
	for i := 0; i < st.NumFields(); i++ {
		field := st.Field(i)
		if field == nil {
			continue
		}
		names[field.Name()] = true
		if field.Embedded() {
			collectNestedFieldNames(field.Type(), seen, names)
		}
	}
}

func structFieldAt(root types.Type, index []int) (*types.Var, string, bool) {
	cur := root
	for pos, idx := range index {
		st := detailStructUnderlying(cur)
		if st == nil || idx < 0 || idx >= st.NumFields() {
			return nil, "", false
		}
		field := st.Field(idx)
		if pos == len(index)-1 {
			return field, st.Tag(idx), true
		}
		cur = field.Type()
	}
	return nil, "", false
}

func fieldOwnerID(root types.Type, index []int) string {
	cur := root
	for pos, idx := range index {
		if pos == len(index)-1 {
			return detailNamedTypeID(detailDerefAlias(cur))
		}
		st := detailStructUnderlying(cur)
		if st == nil || idx < 0 || idx >= st.NumFields() {
			return ""
		}
		cur = st.Field(idx).Type()
	}
	return ""
}

func promotedPath(root types.Type, index []int, q types.Qualifier) string {
	if len(index) < 2 {
		return ""
	}
	parts := make([]string, 0, len(index)-1)
	cur := root
	for _, idx := range index[:len(index)-1] {
		st := detailStructUnderlying(cur)
		if st == nil || idx < 0 || idx >= st.NumFields() {
			return ""
		}
		field := st.Field(idx)
		parts = append(parts, types.TypeString(field.Type(), q))
		cur = field.Type()
	}
	return strings.Join(parts, ".")
}

func lookupFieldOrMethod(t types.Type, pkg *types.Package, name string) (obj types.Object, index []int, ok bool) {
	defer func() {
		if recover() != nil {
			obj, index, ok = nil, nil, false
		}
	}()
	obj, index, _ = types.LookupFieldOrMethod(t, false, pkg, name)
	return obj, index, true
}

func interfaceMethodDetails(iface *types.Interface, q types.Qualifier) ([]InterfaceMethodDetail, []EmbeddedInterface) {
	iface.Complete()
	methods := make([]InterfaceMethodDetail, 0, iface.NumMethods())
	seen := make(map[string]bool, iface.NumMethods())
	for i := 0; i < iface.NumExplicitMethods(); i++ {
		method := iface.ExplicitMethod(i)
		if method == nil || seen[method.Name()] {
			continue
		}
		seen[method.Name()] = true
		methods = append(methods, InterfaceMethodDetail{
			ID:        symbolID(method),
			Name:      method.Name(),
			Signature: types.TypeString(method.Type(), q),
			Exported:  method.Exported(),
		})
	}

	embedded := make([]EmbeddedInterface, 0, iface.NumEmbeddeds())
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		et := iface.EmbeddedType(i)
		embedded = append(embedded, embeddedInterfaceDetail(et, q))
		sub := interfaceOf(et)
		if sub == nil {
			continue
		}
		collectEmbeddedMethods(sub, types.TypeString(et, q), seen, &methods, q)
	}
	return methods, embedded
}

func collectEmbeddedMethods(iface *types.Interface, source string, seen map[string]bool, out *[]InterfaceMethodDetail, q types.Qualifier) {
	iface.Complete()
	for i := 0; i < iface.NumExplicitMethods(); i++ {
		method := iface.ExplicitMethod(i)
		if method == nil || seen[method.Name()] {
			continue
		}
		seen[method.Name()] = true
		*out = append(*out, InterfaceMethodDetail{
			ID:           symbolID(method),
			Name:         method.Name(),
			Signature:    types.TypeString(method.Type(), q),
			EmbeddedFrom: source,
			Exported:     method.Exported(),
		})
	}
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		if sub := interfaceOf(iface.EmbeddedType(i)); sub != nil {
			collectEmbeddedMethods(sub, source, seen, out, q)
		}
	}
}

func embeddedInterfaceDetail(t types.Type, q types.Qualifier) EmbeddedInterface {
	out := EmbeddedInterface{Type: types.TypeString(t, q), Kind: "constraint"}
	if iface := interfaceOf(t); iface != nil && iface.IsMethodSet() {
		out.Kind = "interface"
	}
	switch x := types.Unalias(t).(type) {
	case *types.Named:
		out.ID = detailTypeNameID(x.Obj())
		out.Name = x.Obj().Name()
		out.Exported = x.Obj().Exported()
	case *types.Alias:
		out.ID = detailTypeNameID(x.Obj())
		out.Name = x.Obj().Name()
		out.Exported = x.Obj().Exported()
	default:
		out.Exported = true
	}
	return out
}

func interfaceMethodSet(methods []InterfaceMethodDetail) MethodSetDetail {
	value := make([]MethodDetail, 0, len(methods))
	for _, method := range methods {
		value = append(value, MethodDetail{
			ID: method.ID, Name: method.Name, Signature: method.Signature,
			PromotedFrom: method.EmbeddedFrom, Exported: method.Exported,
		})
	}
	sortMethodDetails(value)
	return MethodSetDetail{Value: value, Pointer: []MethodDetail{}}
}

func typeMethodSet(root types.Type, q types.Qualifier) (out MethodSetDetail) {
	defer func() {
		if recover() != nil {
			out = MethodSetDetail{Value: []MethodDetail{}, Pointer: []MethodDetail{}}
		}
	}()
	valueSet := types.NewMethodSet(root)
	value := make([]MethodDetail, 0, valueSet.Len())
	valueIDs := make(map[string]bool, valueSet.Len())
	for i := 0; i < valueSet.Len(); i++ {
		detail := methodDetail(root, valueSet.At(i), q)
		value = append(value, detail)
		if detail.ID != "" {
			valueIDs[detail.ID] = true
		}
	}

	pointer := make([]MethodDetail, 0)
	pointerSet := types.NewMethodSet(types.NewPointer(root))
	for i := 0; i < pointerSet.Len(); i++ {
		detail := methodDetail(root, pointerSet.At(i), q)
		if valueIDs[detail.ID] && detail.ID != "" {
			continue
		}
		pointer = append(pointer, detail)
	}
	sortMethodDetails(value)
	sortMethodDetails(pointer)
	return MethodSetDetail{Value: value, Pointer: pointer}
}

func methodDetail(root types.Type, selection *types.Selection, q types.Qualifier) MethodDetail {
	fn, _ := selection.Obj().(*types.Func)
	if fn == nil {
		return MethodDetail{}
	}
	recvPtr := false
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		_, recvPtr = sig.Recv().Type().(*types.Pointer)
	}
	return MethodDetail{
		ID:           symbolID(fn),
		Name:         fn.Name(),
		Signature:    types.TypeString(fn.Type(), q),
		RecvPtr:      recvPtr,
		PromotedFrom: promotedPath(root, selection.Index(), q),
		Exported:     fn.Exported(),
	}
}

func sortMethodDetails(methods []MethodDetail) {
	sort.Slice(methods, func(i, j int) bool {
		if methods[i].Name != methods[j].Name {
			return methods[i].Name < methods[j].Name
		}
		return methods[i].ID < methods[j].ID
	})
}

func detailStructUnderlying(t types.Type) *types.Struct {
	t = detailDerefAlias(t)
	if t == nil {
		return nil
	}
	st, _ := t.Underlying().(*types.Struct)
	return st
}

func interfaceOf(t types.Type) *types.Interface {
	t = detailDerefAlias(t)
	if t == nil {
		return nil
	}
	iface, _ := t.Underlying().(*types.Interface)
	return iface
}

func detailDerefAlias(t types.Type) types.Type {
	for {
		next := types.Unalias(t)
		if ptr, ok := next.(*types.Pointer); ok {
			next = types.Unalias(ptr.Elem())
		}
		if next == t {
			return t
		}
		t = next
	}
}

func detailNamedTypeID(t types.Type) string {
	t = detailDerefAlias(t)
	if t == nil {
		return ""
	}
	switch x := t.(type) {
	case *types.Named:
		return detailTypeNameID(x.Obj())
	case *types.Alias:
		return detailTypeNameID(x.Obj())
	default:
		return ""
	}
}

func detailNamedTypePackage(typesToCheck ...types.Type) *types.Package {
	for _, t := range typesToCheck {
		t = detailDerefAlias(t)
		switch x := t.(type) {
		case *types.Named:
			if x.Obj() != nil {
				return x.Obj().Pkg()
			}
		case *types.Alias:
			if x.Obj() != nil {
				return x.Obj().Pkg()
			}
		}
	}
	return nil
}

func detailTypeNameID(obj *types.TypeName) string {
	if obj == nil {
		return ""
	}
	if obj.Pkg() == nil {
		return "builtin:" + obj.Name()
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// collectConstructors attaches package-level constructor functions to their
// returned named types. Function results are inspected through go/types so
// generic instantiations and aliases resolve to the same origin type.
func (b *builder) collectConstructors(pkgs []*packages.Package) {
	byType := map[string][]ConstructorDetail{}
	for _, p := range pkgs {
		if p == nil || p.Types == nil || !b.inModule(p.PkgPath) {
			continue
		}
		q := qualifier(p.PkgPath)
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			fn, ok := scope.Lookup(name).(*types.Func)
			if !ok {
				continue
			}
			sig, ok := fn.Type().(*types.Signature)
			if !ok || sig.Recv() != nil {
				continue
			}
			results := sig.Results()
			if results.Len() == 0 || results.Len() > 2 {
				continue
			}
			if results.Len() == 2 && !isErrorType(results.At(1).Type()) {
				continue
			}
			first := results.At(0).Type()
			keys := constructorTypeKeys(first)
			if len(keys) == 0 {
				continue
			}
			id := b.symbolIDFor(fn)
			if id == "" {
				continue
			}
			detail := ConstructorDetail{
				ID: id, Name: fn.Name(), PkgPath: p.PkgPath,
				Signature: types.TypeString(fn.Type(), q),
				Returns:   types.TypeString(first, q),
				Exported:  fn.Exported(),
			}
			for _, key := range keys {
				byType[key] = append(byType[key], detail)
			}
		}
	}

	for id, detail := range b.idx.TypeDetails {
		seen := map[string]bool{}
		constructors := make([]ConstructorDetail, 0)
		add := func(items []ConstructorDetail) {
			for _, item := range items {
				if seen[item.ID] {
					continue
				}
				seen[item.ID] = true
				constructors = append(constructors, item)
			}
		}
		add(byType[id])
		if detail.Alias && detail.AliasTargetID != "" {
			add(byType[detail.AliasTargetID])
		}
		sort.Slice(constructors, func(i, j int) bool {
			if constructors[i].Name != constructors[j].Name {
				return constructors[i].Name < constructors[j].Name
			}
			return constructors[i].ID < constructors[j].ID
		})
		detail.Constructors = constructors
		b.idx.TypeDetails[id] = detail
	}
}

func constructorTypeKeys(t types.Type) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	var add func(types.Type)
	add = func(typ types.Type) {
		if typ == nil {
			return
		}
		switch x := typ.(type) {
		case *types.Pointer:
			add(x.Elem())
		case *types.Alias:
			if id := detailTypeNameID(x.Obj()); id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
			add(types.Unalias(x))
		case *types.Named:
			if id := detailTypeNameID(x.Obj()); id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(t)
	return out
}

func isErrorType(t types.Type) bool {
	obj := types.Universe.Lookup("error")
	if obj == nil {
		return false
	}
	return types.Identical(t, obj.Type())
}

// enrichTypeDetailXRefs fills field reference counts after references have
// been collected. Metadata itself stays free of pointers into go/types.
func (b *builder) enrichTypeDetailXRefs() {
	for id, detail := range b.idx.TypeDetails {
		for i := range detail.Fields {
			detail.Fields[i].XRefCount = len(b.idx.XRefs[detail.Fields[i].ID])
		}
		b.idx.TypeDetails[id] = detail
	}
}

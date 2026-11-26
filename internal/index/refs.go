package index

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

func (b *builder) indexReferences(pkgs []*packages.Package, testOnly bool) {
	for _, p := range pkgs {
		pi, ok := b.pkgInfoFor(p)
		if !ok || p.TypesInfo == nil || p.Fset == nil {
			continue
		}
		b.indexPackageReferences(p, pi, testOnly)
	}
}

func (b *builder) indexPackageReferences(p *packages.Package, pi *PkgInfo, testOnly bool) {
	srcMod := pi.ModuleArea
	recordUse := func(id *ast.Ident, fromFunc string) {
		b.recordUse(p, srcMod, id, fromFunc)
	}

	for _, file := range p.Syntax {
		if file == nil {
			continue
		}
		isTest := strings.HasSuffix(p.Fset.Position(file.Pos()).Filename, "_test.go")
		if testOnly && !isTest {
			continue
		}
		b.collectFieldOwners(p, file)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				b.indexFuncBody(p, pi, d, recordUse)
			case *ast.GenDecl:
				b.indexGenDecl(p, d, recordUse)
			}
		}
	}
}

// indexGenDecl records package-level type and var/const initializer uses.
// Initializer calls, composite literals, and function values are attributed to
// the declared variable (or the package init ID when the declaration is blank)
// so call graphs and impact analysis see package initialization work.
func (b *builder) indexGenDecl(p *packages.Package, decl *ast.GenDecl, recordUse func(*ast.Ident, string)) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			inspectIdents(s, func(id *ast.Ident) { recordUse(id, "") })
		case *ast.ValueSpec:
			if s.Type != nil {
				inspectIdents(s.Type, func(id *ast.Ident) { recordUse(id, "") })
			}
			callerID := b.valueSpecOwner(p, s)
			for _, value := range s.Values {
				b.indexPackageValue(p, callerID, value, recordUse)
			}
		default:
			inspectIdents(spec, func(id *ast.Ident) { recordUse(id, "") })
		}
	}
}

func (b *builder) valueSpecOwner(p *packages.Package, spec *ast.ValueSpec) string {
	for _, name := range spec.Names {
		obj := p.TypesInfo.Defs[name]
		if obj == nil || obj.Pkg() == nil || obj.Parent() != p.Types.Scope() {
			continue
		}
		if id := b.symbolIDFor(obj); id != "" && b.idx.Symbols[id] != nil {
			return id
		}
	}
	return p.PkgPath + ".init"
}

func (b *builder) indexPackageValue(p *packages.Package, callerID string, value ast.Expr, recordUse func(*ast.Ident, string)) {
	if value == nil {
		return
	}
	ast.Inspect(value, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			recordUse(x, callerID)
			b.recordFuncValue(p, callerID, x)
		case *ast.CompositeLit:
			b.recordComposite(p, callerID, x)
		case *ast.CallExpr:
			b.recordCall(p, callerID, x)
		}
		return true
	})
}

func (b *builder) recordUse(p *packages.Package, srcMod string, id *ast.Ident, fromFunc string) {
	obj := p.TypesInfo.Uses[id]
	if obj == nil || obj.Pkg() == nil || !b.inModule(obj.Pkg().Path()) {
		return
	}
	targetID := symbolID(obj)
	if field, ok := obj.(*types.Var); ok && field.IsField() {
		owner := b.fieldOwners[id]
		if owner == "" {
			return
		}
		targetID = owner + "." + field.Name()
	}
	target, ok := b.idx.Symbols[targetID]
	if !ok {
		return
	}
	pos := p.Fset.Position(id.Pos())
	test := strings.HasSuffix(pos.Filename, "_test.go")
	b.idx.XRefs[target.ID] = append(b.idx.XRefs[target.ID], XRef{
		TargetID:   target.ID,
		FromPkg:    p.PkgPath,
		FromMod:    srcMod,
		FromFuncID: fromFunc,
		File:       relFile(b.absRoot, pos.Filename),
		Line:       pos.Line,
		Test:       test,
	})
	// Architecture edges describe production structure. Test files routinely
	// import many modules (gate/e2e suites), which would manufacture module
	// cycles that do not exist in production. Test references remain available
	// through XRefs, impact analysis, explain_dependency(include_tests=true),
	// and test coverage.
	if !test && srcMod != "" && target.ModuleArea != "" && srcMod != target.ModuleArea {
		addNested(b.modOut, srcMod, target.ModuleArea, target.Name)
		addNested(b.modIn, target.ModuleArea, srcMod, target.Name)
	}
}

// collectFieldOwners maps each selector/key identifier to the named struct
// that declares the referenced field. Doing it in one pass keeps recordUse
// linear instead of rescanning every file for every identifier.
func (b *builder) collectFieldOwners(p *packages.Package, file *ast.File) {
	if p.TypesInfo == nil {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.SelectorExpr:
			if sel := p.TypesInfo.Selections[x]; sel != nil {
				if field, ok := sel.Obj().(*types.Var); ok && field.IsField() {
					if owner := b.fieldOwnerFor(sel.Recv(), field); owner != "" {
						b.fieldOwners[x.Sel] = owner
					}
				}
			}
		case *ast.CompositeLit:
			litType := p.TypesInfo.TypeOf(x)
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if field, ok := p.TypesInfo.Uses[key].(*types.Var); ok && field.IsField() {
					if owner := b.fieldOwnerFor(litType, field); owner != "" {
						b.fieldOwners[key] = owner
					}
				}
			}
		}
		return true
	})
}

// fieldOwnerFor resolves the declaring struct ID for a field object. Pointer
// identity works within one type-checking universe, but generic instantiations
// and packages loaded in separate passes (workspaces and incremental merges)
// expose distinct *types.Var objects for the same field. Origin normalization
// covers generics; the structural lookup covers export-data boundaries.
func (b *builder) fieldOwnerFor(recv types.Type, field *types.Var) string {
	if field == nil {
		return ""
	}
	if owner := b.fieldVarOwner[field.Origin()]; owner != "" {
		return owner
	}
	if recv == nil {
		return ""
	}
	return declaringFieldTypeID(recv, field.Name(), field.Pkg())
}

// declaringFieldTypeID walks the embedded-field index path returned by
// types.LookupFieldOrMethod to find the struct type that declares name.
func declaringFieldTypeID(recv types.Type, name string, pkg *types.Package) string {
	obj, index, _ := types.LookupFieldOrMethod(recv, true, pkg, name)
	if obj == nil || len(index) == 0 {
		return ""
	}
	cur := recv
	if ptr, ok := cur.(*types.Pointer); ok {
		cur = ptr.Elem()
	}
	for _, i := range index[:len(index)-1] {
		st := structUnderlying(cur)
		if st == nil || i < 0 || i >= st.NumFields() {
			return ""
		}
		cur = st.Field(i).Type()
		if ptr, ok := cur.(*types.Pointer); ok {
			cur = ptr.Elem()
		}
	}
	return namedTypeID(cur)
}

func structUnderlying(t types.Type) *types.Struct {
	switch typ := t.(type) {
	case *types.Named:
		if st, ok := typ.Underlying().(*types.Struct); ok {
			return st
		}
	case *types.Alias:
		if st, ok := typ.Underlying().(*types.Struct); ok {
			return st
		}
	case *types.Struct:
		return typ
	}
	return nil
}

func namedTypeID(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	switch named := t.(type) {
	case *types.Named:
		if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
			return obj.Pkg().Path() + "." + obj.Name()
		}
	case *types.Alias:
		if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
			return obj.Pkg().Path() + "." + obj.Name()
		}
	}
	return ""
}

func (b *builder) indexFuncBody(
	p *packages.Package,
	pi *PkgInfo,
	d *ast.FuncDecl,
	recordUse func(*ast.Ident, string),
) {
	callerID := ""
	if obj := p.TypesInfo.Defs[d.Name]; obj != nil {
		callerID = b.symbolIDFor(obj)
	}
	if d.Type != nil {
		inspectIdents(d.Type, func(id *ast.Ident) { recordUse(id, callerID) })
	}
	if d.Body == nil {
		return
	}

	ast.Inspect(d.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.GoStmt:
			b.countGoroutine(pi)
		case *ast.SelectorExpr:
			if x.Sel.Name == "Ports" {
				b.countPorts(pi)
			}
		case *ast.Ident:
			recordUse(x, callerID)
			b.recordFuncValue(p, callerID, x)
		case *ast.CompositeLit:
			b.recordComposite(p, callerID, x)
		case *ast.CallExpr:
			b.recordCall(p, callerID, x)
		}
		return true
	})
}

func (b *builder) recordComposite(p *packages.Package, callerID string, lit *ast.CompositeLit) {
	if p.TypesInfo == nil || p.Fset == nil {
		return
	}
	typeID := namedTypeID(p.TypesInfo.TypeOf(lit))
	if typeID == "" || b.idx.Symbols[typeID] == nil {
		return
	}
	pos := p.Fset.Position(lit.Pos())
	unkeyed := false
	for _, elt := range lit.Elts {
		if _, ok := elt.(*ast.KeyValueExpr); !ok {
			unkeyed = true
			break
		}
	}
	b.idx.CompositeLiterals[typeID] = append(b.idx.CompositeLiterals[typeID], CompositeSite{
		TypeID:   typeID,
		FromPkg:  p.PkgPath,
		FromMod:  b.pkgModule(p.PkgPath),
		FromFunc: callerID,
		File:     relFile(b.absRoot, pos.Filename),
		Line:     pos.Line,
		Unkeyed:  unkeyed,
		Test:     strings.HasSuffix(pos.Filename, "_test.go"),
	})
}

func (b *builder) recordCall(p *packages.Package, callerID string, call *ast.CallExpr) {
	if callerID == "" {
		return
	}
	id := calledIdent(call.Fun)
	if id == nil {
		return
	}
	b.recordInterfaceDispatch(p, callerID, call)
	fn, ok := p.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || !b.inModule(fn.Pkg().Path()) {
		return
	}
	calleeID := b.symbolIDFor(fn)
	if b.idx.Symbols[calleeID] == nil {
		return
	}
	addPair(b.callees, callerID, calleeID)
	addPair(b.callers, calleeID, callerID)
	b.setEdgeKind(callerID, calleeID, CallEdge{Kind: "direct"})
}

// recordFuncValue records a function or method referenced as a value rather
// than called: callbacks, handler registrations, sort comparators, conversion
// arguments, and dispatch-table entries. The func_value edge keeps call graphs
// and static test reachability complete without inventing a call site.
func (b *builder) recordFuncValue(p *packages.Package, callerID string, id *ast.Ident) {
	if callerID == "" || p.TypesInfo == nil || id == nil {
		return
	}
	fn, ok := p.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn == nil || fn.Pkg() == nil || !b.inModule(fn.Pkg().Path()) {
		return
	}
	calleeID := b.symbolIDFor(fn)
	if calleeID == "" || b.idx.Symbols[calleeID] == nil {
		return
	}
	addPair(b.callees, callerID, calleeID)
	addPair(b.callers, calleeID, callerID)
	b.setEdgeKind(callerID, calleeID, CallEdge{Kind: "func_value"})
}

func (b *builder) setEdgeKind(from, to string, edge CallEdge) {
	key := from + "\x00" + to
	if existing, ok := b.edgeKinds[key]; ok && existing.Kind == "direct" {
		return
	}
	b.edgeKinds[key] = edge
}

// recordInterfaceDispatch expands an in-module interface method call to the
// concrete implementations known from go/types. Ubiquitous stdlib interfaces
// are not in b.ifaceOwner, so they are never expanded.
func (b *builder) recordInterfaceDispatch(p *packages.Package, callerID string, call *ast.CallExpr) {
	sel := selectorExpr(call.Fun)
	if sel == nil || p.TypesInfo == nil {
		return
	}
	selection := p.TypesInfo.Selections[sel]
	if selection == nil {
		return
	}
	if _, ok := selection.Recv().Underlying().(*types.Interface); !ok {
		return
	}
	fn, ok := selection.Obj().(*types.Func)
	if !ok || fn == nil {
		return
	}
	var ifaceIDs []string
	if id := namedTypeID(selection.Recv()); id != "" {
		if sym := b.idx.Symbols[id]; sym != nil && sym.Kind == "interface" {
			ifaceIDs = []string{id}
		}
	}
	if len(ifaceIDs) == 0 {
		if id := b.ifaceOwner[fn]; id != "" {
			ifaceIDs = []string{id}
		} else if fn.Pkg() != nil {
			ifaceIDs = b.ifaceMethod[fn.Pkg().Path()+"."+fn.Name()]
		}
	}
	for _, ifaceID := range ifaceIDs {
		b.pendingDispatch = append(b.pendingDispatch, dispatchCall{
			caller: callerID, iface: ifaceID, method: fn.Name(),
		})
	}
}

type dispatchCall struct {
	caller string
	iface  string
	method string
}

// applyDispatchEdges expands interface call sites after the implementation
// matrix is complete. References are indexed before matchInterfaces runs.
func (b *builder) applyDispatchEdges() {
	for _, call := range b.pendingDispatch {
		for _, match := range b.idx.IfaceImpls[call.iface] {
			methodID := match.MethodIDs[call.method]
			if methodID == "" {
				// Compatibility for indexes built before exact method maps
				// existed: fall back to the concrete receiver's method ID.
				methodID = match.TypeID + "." + call.method
			}
			if b.idx.Symbols[methodID] == nil {
				continue
			}
			addPair(b.callees, call.caller, methodID)
			addPair(b.callers, methodID, call.caller)
			b.setEdgeKind(call.caller, methodID, CallEdge{Kind: "interface_dispatch", Via: call.iface})
		}
	}
}

func selectorExpr(expr ast.Expr) *ast.SelectorExpr {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.SelectorExpr:
			return e
		default:
			return nil
		}
	}
}

// collectImportSites records every import declaration, including blank and dot
// imports. It runs for production and test packages.
func (b *builder) collectImportSites(pkgs []*packages.Package, testOnly bool) {
	for _, p := range pkgs {
		if p.Fset == nil {
			continue
		}
		for _, file := range p.Syntax {
			if file == nil {
				continue
			}
			path := p.Fset.Position(file.Pos()).Filename
			isTest := strings.HasSuffix(path, "_test.go")
			if testOnly && !isTest {
				continue
			}
			for _, spec := range file.Imports {
				toPkg := strings.Trim(spec.Path.Value, `"`)
				if !b.inModule(toPkg) {
					continue
				}
				pos := p.Fset.Position(spec.Pos())
				site := ImportSite{
					FromPkg: p.PkgPath,
					ToPkg:   toPkg,
					File:    relFile(b.absRoot, pos.Filename),
					Line:    pos.Line,
					Test:    isTest,
				}
				if spec.Name != nil {
					site.Name = spec.Name.Name
					switch spec.Name.Name {
					case "_":
						site.Blank = true
					case ".":
						site.Dot = true
					default:
						site.Alias = spec.Name.Name
					}
				}
				b.idx.ImportSites = append(b.idx.ImportSites, site)
			}
		}
	}
}

func (b *builder) countGoroutine(pi *PkgInfo) {
	pi.Goroutines++
	if pi.ModuleArea != "" {
		b.module(pi.ModuleArea).Goroutines++
	}
}

func (b *builder) countPorts(pi *PkgInfo) {
	pi.PortsCalls++
	if pi.ModuleArea != "" {
		b.module(pi.ModuleArea).PortsCalls++
	}
}

func (b *builder) pkgModule(pkgPath string) string {
	if pi := b.idx.Packages[pkgPath]; pi != nil {
		return pi.ModuleArea
	}
	return ""
}

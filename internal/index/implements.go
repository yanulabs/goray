package index

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
)

type concreteType struct {
	id    string
	named *types.Named
	sym   *Symbol
	value *types.MethodSet
	ptr   *types.MethodSet
}

type ifaceType struct {
	id    string
	iface *types.Interface
	sym   *Symbol
}

func newConcreteType(id string, named *types.Named, sym *Symbol) concreteType {
	return concreteType{
		id:    id,
		named: named,
		sym:   sym,
		value: types.NewMethodSet(named),
		ptr:   types.NewMethodSet(types.NewPointer(named)),
	}
}

// collectImportedInterfaces gathers every non-empty interface visible to the
// module: module-local declarations, exported interfaces from directly imported
// packages (stdlib and third-party), and the predeclared error interface.
// Empty interfaces are excluded because any type satisfies them.
func (b *builder) collectImportedInterfaces(pkgs []*packages.Package) {
	seen := map[string]bool{}
	add := func(id string, iface *types.Interface, sym *Symbol) {
		if iface == nil || iface.NumMethods() == 0 || seen[id] {
			return
		}
		seen[id] = true
		b.visibleIfaces = append(b.visibleIfaces, ifaceType{id: id, iface: iface, sym: sym})
	}

	for _, local := range b.ifaces {
		add(local.id, local.iface, local.sym)
	}
	if obj := types.Universe.Lookup("error"); obj != nil {
		if tn, ok := obj.(*types.TypeName); ok {
			if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
				add("builtin:error", iface, &Symbol{ID: "builtin:error", Name: "error"})
			}
		}
	}
	for _, p := range pkgs {
		if !b.inModule(p.PkgPath) || p.Types == nil {
			continue
		}
		for _, imported := range p.Types.Imports() {
			if imported == nil {
				continue
			}
			impPath := imported.Path()
			scope := imported.Scope()
			for _, name := range scope.Names() {
				if !token.IsExported(name) {
					continue
				}
				tn, ok := scope.Lookup(name).(*types.TypeName)
				if !ok {
					continue
				}
				iface, ok := tn.Type().Underlying().(*types.Interface)
				if !ok || iface.NumMethods() == 0 {
					continue
				}
				add(impPath+"."+name, iface, &Symbol{ID: impPath + "." + name, Name: name, PkgPath: impPath})
			}
		}
	}
}

// safeImplements guards against types.Implements panics on uninstantiated
// generic or otherwise incomplete types.
func safeImplements(t types.Type, iface *types.Interface) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return types.Implements(t, iface)
}

// markInterfaceMethod records the concrete method that satisfies an interface
// method and returns the exact method symbol IDs keyed by required method name.
// Interface methods may be declared in another package, so lookup by name only
// (pkg == nil). Promoted methods on embedded structs resolve to the declaring
// embedded type, which is what call graphs and impact analysis must follow.
func markInterfaceMethod(methods *types.MethodSet, iface *types.Interface, idx *Index) map[string]string {
	resolved := make(map[string]string, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		want := iface.Method(i)
		if sel := methods.Lookup(nil, want.Name()); sel != nil {
			if fn, ok := sel.Obj().(*types.Func); ok {
				id := symbolID(fn)
				idx.IfaceMethods[id] = true
				resolved[want.Name()] = id
			}
		}
	}
	return resolved
}

// matchInterfaces builds the exact go/types implementation matrix for
// module-local interfaces, then suppresses dead-code false positives for every
// visible interface (including stdlib/imported ones such as fmt.Stringer).
func (b *builder) matchInterfaces() {
	for _, iface := range b.ifaces {
		required := iface.iface.NumMethods()
		for _, typ := range b.concrete {
			if typ.named != nil && typ.named.TypeParams().Len() > 0 {
				continue // uninstantiated generics: types.Implements is undefined
			}
			if typ.ptr.Len() < required {
				continue
			}
			viaValue := typ.value.Len() >= required && safeImplements(typ.named, iface.iface)
			viaPtr := !viaValue && safeImplements(types.NewPointer(typ.named), iface.iface)
			if !viaValue && !viaPtr {
				continue
			}

			match := ImplMatch{
				IfaceID:   iface.id,
				TypeID:    typ.id,
				ViaPtr:    viaPtr,
				TypeFile:  typ.sym.File,
				TypeLine:  typ.sym.Line,
				IfaceFile: iface.sym.File,
				IfaceLine: iface.sym.Line,
			}
			methods := typ.value
			if viaPtr {
				methods = typ.ptr
			}
			match.MethodIDs = markInterfaceMethod(methods, iface.iface, b.idx)
			b.idx.IfaceImpls[iface.id] = append(b.idx.IfaceImpls[iface.id], match)
			b.idx.TypeImpls[typ.id] = append(b.idx.TypeImpls[typ.id], match)
		}
	}

	for _, typ := range b.concrete {
		if typ.named != nil && typ.named.TypeParams().Len() > 0 {
			continue // uninstantiated generics: skip types.Implements paths
		}
		for _, iface := range b.visibleIfaces {
			if typ.ptr.Len() < iface.iface.NumMethods() {
				continue
			}
			viaValue := safeImplements(typ.named, iface.iface)
			viaPtr := !viaValue && safeImplements(types.NewPointer(typ.named), iface.iface)
			if !viaValue && !viaPtr {
				continue
			}
			methods := typ.value
			if viaPtr {
				methods = typ.ptr
			}
			markInterfaceMethod(methods, iface.iface, b.idx)
		}
	}
}

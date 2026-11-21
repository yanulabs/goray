package index

import "go/types"

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

// matchInterfaces builds the exact go/types implementation matrix.
func (b *builder) matchInterfaces() {
	for _, iface := range b.ifaces {
		required := iface.iface.NumMethods()
		for _, typ := range b.concrete {
			if typ.ptr.Len() < required {
				continue
			}
			viaValue := typ.value.Len() >= required && types.Implements(typ.named, iface.iface)
			viaPtr := !viaValue && types.Implements(types.NewPointer(typ.named), iface.iface)
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
			b.idx.IfaceImpls[iface.id] = append(b.idx.IfaceImpls[iface.id], match)
			b.idx.TypeImpls[typ.id] = append(b.idx.TypeImpls[typ.id], match)

			methods := typ.value
			if viaPtr {
				methods = typ.ptr
			}
			for i := 0; i < required; i++ {
				want := iface.iface.Method(i)
				if sel := methods.Lookup(want.Pkg(), want.Name()); sel != nil {
					if fn, ok := sel.Obj().(*types.Func); ok {
						b.idx.IfaceMethods[symbolID(fn)] = true
					}
				}
			}
		}
	}
}

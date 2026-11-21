package index

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

func (b *builder) indexDefinitions(p *packages.Package, pi *PkgInfo) {
	if p.Types == nil || p.TypesInfo == nil || p.Fset == nil {
		return
	}
	for _, file := range p.Syntax {
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				b.indexFunc(p, pi, d)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						b.indexType(p, pi, s)
					case *ast.ValueSpec:
						b.indexValues(p, pi, s)
					}
				}
			}
		}
	}
}

func (b *builder) indexFunc(p *packages.Package, pi *PkgInfo, d *ast.FuncDecl) {
	fn, ok := p.TypesInfo.Defs[d.Name].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}
	id := symbolID(fn)
	if id == "" {
		return
	}

	kind, recv := "func", ""
	recvPtr := false
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		kind = "method"
		recv = recvTypeName(sig.Recv().Type())
		_, recvPtr = sig.Recv().Type().(*types.Pointer)
	}
	pos := p.Fset.Position(d.Pos())
	end := p.Fset.Position(d.End())
	b.addSymbol(&Symbol{
		ID:         id,
		Name:       fn.Name(),
		Kind:       kind,
		PkgPath:    p.PkgPath,
		PkgRel:     pi.RelPath,
		ModuleArea: pi.ModuleArea,
		RecvType:   recv,
		RecvPtr:    recvPtr,
		Exported:   fn.Exported() && (recv == "" || ast.IsExported(recv)),
		Signature:  types.TypeString(fn.Type(), qualifier(p.PkgPath)),
		File:       relFile(b.absRoot, pos.Filename),
		Line:       pos.Line,
		EndLine:    end.Line,
		LOC:        end.Line - pos.Line + 1,
		Cyclomatic: cyclomatic(d.Body),
	}, pi)
}

func (b *builder) indexType(p *packages.Package, pi *PkgInfo, spec *ast.TypeSpec) {
	tn, ok := p.TypesInfo.Defs[spec.Name].(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return
	}
	id := symbolID(tn)
	pos := p.Fset.Position(spec.Pos())
	end := p.Fset.Position(spec.End())
	kind := "type"
	switch tn.Type().Underlying().(type) {
	case *types.Interface:
		kind = "interface"
	case *types.Struct:
		kind = "struct"
	}
	sym := &Symbol{
		ID:         id,
		Name:       tn.Name(),
		Kind:       kind,
		PkgPath:    p.PkgPath,
		PkgRel:     pi.RelPath,
		ModuleArea: pi.ModuleArea,
		Exported:   tn.Exported(),
		Signature:  typeDesc(tn),
		File:       relFile(b.absRoot, pos.Filename),
		Line:       pos.Line,
		EndLine:    end.Line,
		LOC:        end.Line - pos.Line + 1,
	}
	b.addSymbol(sym, pi)

	if iface, ok := tn.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
		b.ifaces = append(b.ifaces, ifaceType{id: id, iface: iface, sym: sym})
		return
	}
	if named, ok := tn.Type().(*types.Named); ok {
		b.concrete = append(b.concrete, newConcreteType(id, named, sym))
	}
}

func (b *builder) indexValues(p *packages.Package, pi *PkgInfo, spec *ast.ValueSpec) {
	for _, name := range spec.Names {
		obj := p.TypesInfo.Defs[name]
		if obj == nil || obj.Pkg() == nil || obj.Parent() != p.Types.Scope() {
			continue
		}
		kind := "var"
		if _, ok := obj.(*types.Const); ok {
			kind = "const"
		}
		pos := p.Fset.Position(name.Pos())
		b.addSymbol(&Symbol{
			ID:         symbolID(obj),
			Name:       obj.Name(),
			Kind:       kind,
			PkgPath:    p.PkgPath,
			PkgRel:     pi.RelPath,
			ModuleArea: pi.ModuleArea,
			Exported:   obj.Exported(),
			Signature:  types.TypeString(obj.Type(), qualifier(p.PkgPath)),
			File:       relFile(b.absRoot, pos.Filename),
			Line:       pos.Line,
		}, pi)
	}
}

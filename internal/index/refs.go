package index

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

func (b *builder) indexReferences(pkgs []*packages.Package) {
	for _, p := range pkgs {
		pi, ok := b.idx.Packages[p.PkgPath]
		if !ok || p.TypesInfo == nil || p.Fset == nil {
			continue
		}
		b.indexPackageReferences(p, pi)
	}
}

func (b *builder) indexPackageReferences(p *packages.Package, pi *PkgInfo) {
	srcMod := pi.ModuleArea

	recordUse := func(id *ast.Ident, fromFunc string) {
		obj := p.TypesInfo.Uses[id]
		if obj == nil || obj.Pkg() == nil || !b.inModule(obj.Pkg().Path()) {
			return
		}
		target, ok := b.idx.Symbols[symbolID(obj)]
		if !ok {
			return
		}
		pos := p.Fset.Position(id.Pos())
		b.idx.XRefs[target.ID] = append(b.idx.XRefs[target.ID], XRef{
			TargetID:   target.ID,
			FromPkg:    p.PkgPath,
			FromMod:    srcMod,
			FromFuncID: fromFunc,
			File:       relFile(b.absRoot, pos.Filename),
			Line:       pos.Line,
		})
		if srcMod != "" && target.ModuleArea != "" && srcMod != target.ModuleArea {
			addNested(b.modOut, srcMod, target.ModuleArea, target.Name)
			addNested(b.modIn, target.ModuleArea, srcMod, target.Name)
		}
	}

	for _, file := range p.Syntax {
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				b.indexFuncBody(p, pi, d, recordUse)
			case *ast.GenDecl:
				ast.Inspect(d, func(node ast.Node) bool {
					if id, ok := node.(*ast.Ident); ok {
						recordUse(id, "")
					}
					return true
				})
			}
		}
	}
}

func (b *builder) indexFuncBody(
	p *packages.Package,
	pi *PkgInfo,
	d *ast.FuncDecl,
	recordUse func(*ast.Ident, string),
) {
	callerID := ""
	if obj := p.TypesInfo.Defs[d.Name]; obj != nil {
		callerID = symbolID(obj)
	}
	if d.Type != nil {
		ast.Inspect(d.Type, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok {
				recordUse(id, callerID)
			}
			return true
		})
	}
	if d.Body == nil {
		return
	}

	ast.Inspect(d.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.GoStmt:
			pi.Goroutines++
			if pi.ModuleArea != "" {
				b.module(pi.ModuleArea).Goroutines++
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "Ports" {
				pi.PortsCalls++
				if pi.ModuleArea != "" {
					b.module(pi.ModuleArea).PortsCalls++
				}
			}
		case *ast.Ident:
			recordUse(x, callerID)
		case *ast.CallExpr:
			if callerID == "" {
				break
			}
			id := calledIdent(x.Fun)
			if id == nil {
				break
			}
			fn, ok := p.TypesInfo.Uses[id].(*types.Func)
			if !ok || fn.Pkg() == nil || !b.inModule(fn.Pkg().Path()) {
				break
			}
			calleeID := symbolID(fn)
			if _, exists := b.idx.Symbols[calleeID]; !exists {
				break
			}
			addPair(b.callees, callerID, calleeID)
			addPair(b.callers, calleeID, callerID)
		}
		return true
	})
}

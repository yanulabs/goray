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
		b.recordUse(p, srcMod, id, fromFunc)
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
				inspectIdents(d, func(id *ast.Ident) { recordUse(id, "") })
			}
		}
	}
}

func (b *builder) recordUse(p *packages.Package, srcMod string, id *ast.Ident, fromFunc string) {
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
		case *ast.CallExpr:
			b.recordCall(p, callerID, x)
		}
		return true
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
	fn, ok := p.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || !b.inModule(fn.Pkg().Path()) {
		return
	}
	calleeID := symbolID(fn)
	if b.idx.Symbols[calleeID] == nil {
		return
	}
	addPair(b.callees, callerID, calleeID)
	addPair(b.callers, calleeID, callerID)
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

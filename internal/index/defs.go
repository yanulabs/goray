package index

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// fileMeta carries provenance computed once per syntax file and shared by all
// symbols declared in it.
type fileMeta struct {
	generated bool
	untracked bool
	isTest    bool
}

func (b *builder) indexDefinitions(p *packages.Package, pi *PkgInfo) {
	if p.Types == nil || p.TypesInfo == nil || p.Fset == nil {
		return
	}
	for _, file := range p.Syntax {
		if file == nil {
			continue
		}
		meta := b.fileMetaFor(p, file)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				b.indexFunc(p, pi, d, meta)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						b.indexType(p, pi, s, meta)
					case *ast.ValueSpec:
						b.indexValues(p, pi, s, meta)
					}
				}
			}
		}
	}
}

func (b *builder) fileMetaFor(p *packages.Package, file *ast.File) fileMeta {
	path := ""
	if p.Fset != nil && file.Pos().IsValid() {
		path = p.Fset.Position(file.Pos()).Filename
	}
	meta := fileMeta{
		isTest:    strings.HasSuffix(path, "_test.go"),
		untracked: b.idx.UntrackedFiles[relFile(b.absRoot, path)],
	}
	if path != "" {
		if src, err := os.ReadFile(path); err == nil {
			meta.generated = isGenerated(filepath.Base(path), src)
		}
	}
	return meta
}

func (b *builder) indexFunc(p *packages.Package, pi *PkgInfo, d *ast.FuncDecl, meta fileMeta) {
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
	name := fn.Name()
	b.addSymbol(&Symbol{
		ID:         id,
		Name:       name,
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
		Generated:  meta.generated,
		Untracked:  meta.untracked,
		IsTest:     meta.isTest,
		IsHook:     isHookName(name, meta.isTest) || hasHookDirective(d.Doc),
	}, pi)
}

func isHookName(name string, inTest bool) bool {
	if name == "main" || name == "init" || isStdHook(name) {
		return true
	}
	if !inTest {
		return false
	}
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (b *builder) indexType(p *packages.Package, pi *PkgInfo, spec *ast.TypeSpec, meta fileMeta) {
	tn, ok := p.TypesInfo.Defs[spec.Name].(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return
	}
	id := symbolID(tn)
	pos := p.Fset.Position(spec.Pos())
	end := p.Fset.Position(spec.End())
	kind := "type"
	fields := 0
	switch u := tn.Type().Underlying().(type) {
	case *types.Interface:
		kind = "interface"
	case *types.Struct:
		kind = "struct"
		fields = u.NumFields()
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
		Fields:     fields,
		Generated:  meta.generated,
		Untracked:  meta.untracked,
		IsTest:     meta.isTest,
	}
	if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
		sym.Methods = iface.NumMethods()
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

func (b *builder) indexValues(p *packages.Package, pi *PkgInfo, spec *ast.ValueSpec, meta fileMeta) {
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
			Generated:  meta.generated,
			Untracked:  meta.untracked,
			IsTest:     meta.isTest,
		}, pi)
	}
}

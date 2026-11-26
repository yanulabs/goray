package index

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
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

// fileFacts is the on-disk metadata shared by definition indexing, summary
// building, and file metrics so every file is read at most once.
type fileFacts struct {
	loc       int
	generated bool
}

func (b *builder) factsFor(path string) (fileFacts, error) {
	if facts, ok := b.facts[path]; ok {
		return facts, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return fileFacts{}, err
	}
	facts := fileFacts{loc: countLines(src), generated: isGenerated(filepath.Base(path), src)}
	b.facts[path] = facts
	return facts, nil
}

func (b *builder) indexDefinitions(p *packages.Package, pi *PkgInfo, testOnly bool) {
	if p.Types == nil || p.TypesInfo == nil || p.Fset == nil {
		return
	}
	for _, file := range sortedSyntaxFiles(p, p.Syntax) {
		if file == nil {
			continue
		}
		meta := b.fileMetaFor(p, file)
		if testOnly && !meta.isTest {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				b.indexFunc(p, pi, d, meta)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						b.indexType(p, pi, d, s, meta)
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
		if facts, err := b.factsFor(path); err == nil {
			meta.generated = facts.generated
		}
	}
	return meta
}

// sortedSyntaxFiles orders syntax by file position so disambiguated IDs are
// stable regardless of loader order.
func sortedSyntaxFiles(p *packages.Package, files []*ast.File) []*ast.File {
	out := append([]*ast.File{}, files...)
	if p.Fset == nil {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == nil || out[j] == nil {
			return out[j] != nil
		}
		pi := p.Fset.Position(out[i].Pos())
		pj := p.Fset.Position(out[j].Pos())
		if pi.Filename != pj.Filename {
			return pi.Filename < pj.Filename
		}
		return pi.Line < pj.Line
	})
	return out
}

func (b *builder) indexFunc(p *packages.Package, pi *PkgInfo, d *ast.FuncDecl, meta fileMeta) {
	fn, ok := p.TypesInfo.Defs[d.Name].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}
	id := b.allocateFuncID(fn)
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
	sym := &Symbol{
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
	}
	if base := symbolID(fn); id != base {
		sym.canonicalID = "sym:" + id
	}
	b.addSymbol(sym, pi)
}

// allocateFuncID assigns each function declaration one ID. Repeated init
// functions in a package are ordered by file and line, keeping the historical
// pkg.init ID for the first and appending #2, #3, ... for the rest.
func (b *builder) allocateFuncID(fn *types.Func) string {
	if id, ok := b.symbolIDs[fn]; ok {
		return id
	}
	base := symbolID(fn)
	if base == "" {
		return ""
	}
	id := base
	if fn.Name() == "init" {
		for n := 2; b.idx.Symbols[id] != nil; n++ {
			id = fmt.Sprintf("%s#%d", base, n)
		}
	}
	b.symbolIDs[fn] = id
	return id
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

func (b *builder) indexType(p *packages.Package, pi *PkgInfo, gen *ast.GenDecl, spec *ast.TypeSpec, meta fileMeta) {
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
	b.addTypeDetail(p, gen, spec, tn, sym)

	if structType, ok := tn.Type().Underlying().(*types.Struct); ok {
		// A type alias shares the original type's *types.Var field objects.
		// Re-indexing them under the alias package would create duplicate field
		// symbols and overwrite the declaring type's ownership, inventing
		// module dependencies and cycles.
		if _, alias := tn.Type().(*types.Alias); !alias {
			b.indexFields(p, pi, tn, structType, meta)
		}
	}

	if iface, ok := tn.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
		b.ifaces = append(b.ifaces, ifaceType{id: id, iface: iface, sym: sym})
		b.registerInterfaceMethods(id, iface)
		return
	}
	if named, ok := tn.Type().(*types.Named); ok {
		b.concrete = append(b.concrete, newConcreteType(id, named, sym))
	}
}

// indexFields gives struct fields first-class symbols so references resolve
// through go/types rather than name matching. The ID is pkg.Type.Field.
func (b *builder) indexFields(p *packages.Package, pi *PkgInfo, tn *types.TypeName, structType *types.Struct, meta fileMeta) {
	typeID := symbolID(tn)
	for i := 0; i < structType.NumFields(); i++ {
		field := structType.Field(i)
		if field == nil || field.Name() == "" {
			continue
		}
		pos := p.Fset.Position(field.Pos())
		origin := field.Origin()
		if _, owned := b.fieldVarOwner[origin]; !owned {
			b.fieldVarOwner[origin] = typeID
		}
		b.addSymbol(&Symbol{
			ID:         typeID + "." + field.Name(),
			Name:       field.Name(),
			Kind:       "field",
			PkgPath:    p.PkgPath,
			PkgRel:     pi.RelPath,
			ModuleArea: pi.ModuleArea,
			RecvType:   tn.Name(),
			Exported:   field.Exported(),
			Signature:  types.TypeString(field.Type(), qualifier(p.PkgPath)),
			File:       relFile(b.absRoot, pos.Filename),
			Line:       pos.Line,
			Generated:  meta.generated,
			Untracked:  meta.untracked,
			IsTest:     meta.isTest,
		}, pi)
	}
}

// registerInterfaceMethods maps interface method objects to their interface ID
// so call sites can be expanded to concrete implementations by CHA.
func (b *builder) registerInterfaceMethods(id string, iface *types.Interface) {
	for i := 0; i < iface.NumMethods(); i++ {
		method := iface.Method(i)
		if method == nil {
			continue
		}
		b.ifaceOwner[method] = id
		// Methods declared in anonymous/embedded interface literals can have a
		// nil package. The call-site resolver also has the receiver type ID, so
		// this name-based fallback is optional and must not panic.
		pkgPath := ""
		if method.Pkg() != nil {
			pkgPath = method.Pkg().Path()
		}
		key := pkgPath + "." + method.Name()
		b.ifaceMethod[key] = append(b.ifaceMethod[key], id)
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

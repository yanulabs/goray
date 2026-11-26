package index

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

func (b *builder) summaryFunc(
	p *packages.Package,
	file *ast.File,
	d *ast.FuncDecl,
	fileRec *summaryFile,
	decls map[string]*summaryDecl,
) {
	fn, ok := p.TypesInfo.Defs[d.Name].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}
	existingID := symbolID(fn)
	sym := b.idx.Symbols[existingID]
	if sym == nil {
		return
	}
	canonical := sym.CanonicalID()
	if canonical == "" || decls[canonical] != nil {
		return
	}

	doc := ""
	docStart := 0
	if d.Doc != nil {
		doc = d.Doc.Text()
		docStart = p.Fset.Position(d.Doc.Pos()).Line
	}
	directives := nodeDirectives(file, d, d.Doc)
	body := ""
	if d.Body != nil {
		body = printNode(d.Body)
	}
	sigHash := b.canonicalSignatureHash(printNode(d.Type), fn.Type())
	selfHash := hashParts(sigHash, hashText(body), hashText(normalizeDoc(doc)), hashText(strings.Join(directives, "\n")))

	typeRefs := b.collectTypeRefs(p, d, canonical)
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		if id := canonicalNamedType(sig.Recv().Type()); id != "" && id != canonical {
			typeRefs[id] = struct{}{}
		}
	}

	ss := &SummarySymbol{
		ID: canonical, ParentID: fileRec.sym.ID, Kind: sym.Kind,
		ModuleName: sym.ModuleArea, PkgPath: sym.PkgPath, FilePath: sym.File,
		StartLine: sym.Line, EndLine: sym.EndLine, LOC: sym.LOC, Complexity: sym.Cyclomatic,
		Exported: sym.Exported, SigHash: sigHash, SelfHash: selfHash,
		IsTest: fileRec.sym.IsTest, IsGenerated: fileRec.sym.IsGenerated,
		Doc: doc, Signature: sym.Signature, Directives: directives, DocStartLine: docStart,
	}
	decls[canonical] = &summaryDecl{
		sym: ss, existingID: existingID,
		calleeIDs: append([]string{}, b.idx.Callees[existingID]...),
		typeRefs:  typeRefs, recvType: sym.RecvType,
	}
}

func (b *builder) summaryType(
	p *packages.Package,
	file *ast.File,
	gen *ast.GenDecl,
	spec *ast.TypeSpec,
	fileRec *summaryFile,
	decls map[string]*summaryDecl,
	typesByID map[string]*summaryType,
) {
	tn, ok := p.TypesInfo.Defs[spec.Name].(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return
	}
	existingID := symbolID(tn)
	sym := b.idx.Symbols[existingID]
	if sym == nil {
		return
	}
	canonical := sym.CanonicalID()
	if canonical == "" || decls[canonical] != nil {
		return
	}

	docs := make([]string, 0, 2)
	docStart := 0
	if gen.Doc != nil {
		docs = append(docs, gen.Doc.Text())
		docStart = p.Fset.Position(gen.Doc.Pos()).Line
	}
	if spec.Doc != nil {
		docs = append(docs, spec.Doc.Text())
		if line := p.Fset.Position(spec.Doc.Pos()).Line; docStart == 0 || line < docStart {
			docStart = line
		}
	}
	doc := normalizeDoc(strings.Join(docs, "\n"))
	directives := nodeDirectives(file, spec, gen.Doc, spec.Doc)
	sigHash := b.canonicalSignatureHash(printNode(spec.Type), tn.Type())
	selfHash := hashParts(sigHash, hashText(doc), hashText(strings.Join(directives, "\n")))

	shape := &summaryType{sym: nil, existingID: existingID}
	embedded := make([]string, 0)
	switch underlying := tn.Type().Underlying().(type) {
	case *types.Interface:
		shape.isInterface = true
		for i := 0; i < underlying.NumEmbeddeds(); i++ {
			if id := canonicalNamedType(underlying.EmbeddedType(i)); id != "" {
				embedded = append(embedded, id)
			}
		}
	case *types.Struct:
		shape.structFields = underlying.NumFields()
		for i := 0; i < underlying.NumFields(); i++ {
			if underlying.Field(i).Embedded() {
				if id := canonicalNamedType(underlying.Field(i).Type()); id != "" {
					embedded = append(embedded, id)
				}
			}
		}
	}

	ss := &SummarySymbol{
		ID: canonical, ParentID: fileRec.sym.ID, Kind: "type",
		ModuleName: sym.ModuleArea, PkgPath: sym.PkgPath, FilePath: sym.File,
		StartLine: sym.Line, EndLine: sym.EndLine, LOC: sym.LOC,
		Exported: sym.Exported, SigHash: sigHash, SelfHash: selfHash,
		IsTest: fileRec.sym.IsTest, IsGenerated: fileRec.sym.IsGenerated,
		Doc: doc, Signature: sym.Signature, Directives: directives, DocStartLine: docStart,
	}
	decl := &summaryDecl{
		sym: ss, existingID: existingID, isType: true,
		typeRefs: b.collectTypeRefs(p, spec, canonical),
	}
	decls[canonical] = decl
	shape.sym = ss
	shape.embeddedTypes = embedded
	typesByID[canonical] = shape
}

func (b *builder) collectTypeRefs(p *packages.Package, node ast.Node, self string) map[string]struct{} {
	refs := map[string]struct{}{}
	inspectIdents(node, func(id *ast.Ident) {
		obj, ok := p.TypesInfo.Uses[id].(*types.TypeName)
		if !ok || obj.Pkg() == nil || !b.inModule(obj.Pkg().Path()) {
			return
		}
		canonical := "sym:" + obj.Pkg().Path() + "." + obj.Name()
		if canonical != self {
			refs[canonical] = struct{}{}
		}
	})
	return refs
}

func canonicalNamedType(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	var obj *types.TypeName
	switch named := t.(type) {
	case *types.Named:
		obj = named.Obj()
	case *types.Alias:
		obj = named.Obj()
	}
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return "sym:" + obj.Pkg().Path() + "." + obj.Name()
}

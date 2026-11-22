package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

func (b *builder) addTestFileSummaries(
	p *packages.Package,
	pkgRec *summaryPackage,
	data *SummaryData,
	files map[string]*summaryFile,
	decls map[string]*summaryDecl,
	forced map[string]bool,
) {
	pkg := b.idx.Packages[p.PkgPath]
	if pkg == nil || pkg.Dir == "" {
		return
	}
	entries, err := os.ReadDir(pkg.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkg.Dir, entry.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			continue
		}
		filePath := relFile(b.absRoot, path)
		fileID := "file:" + filePath
		if files[fileID] != nil {
			continue
		}
		loc := countLines(src)
		dirs := directiveLines(file.Comments)
		fileSym := &SummarySymbol{
			ID: fileID, ParentID: pkgRec.sym.ID, Kind: "file",
			ModuleName: pkgRec.sym.ModuleName, PkgPath: p.PkgPath,
			FilePath: filePath, StartLine: 1, EndLine: loc, LOC: loc,
			IsTest: true, IsGenerated: isGenerated(filepath.Base(path), src),
			SelfHash:   hashText(strings.Join(dirs, "\n")),
			Directives: dirs,
		}
		data.Symbols = append(data.Symbols, fileSym)
		data.ByID[fileID] = fileSym
		fileRec := &summaryFile{sym: fileSym}
		files[fileID] = fileRec
		pkgRec.files = append(pkgRec.files, fileRec)
		forced[fileID] = true

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				recv, ptr := astRecvType(d)
				canonical := testCanonicalID(p.PkgPath, recv, ptr, d.Name.Name)
				if canonical == "" || decls[canonical] != nil {
					continue
				}
				pos := fset.Position(d.Pos())
				end := fset.Position(d.End())
				doc := ""
				if d.Doc != nil {
					doc = d.Doc.Text()
				}
				directives := nodeDirectives(file, d, d.Doc)
				body := ""
				if d.Body != nil {
					body = printNode(d.Body)
				}
				sig := printNode(d.Type)
				sigHash := hashText(sig)
				selfHash := hashParts(sigHash, hashText(body), hashText(normalizeDoc(doc)), hashText(strings.Join(directives, "\n")))
				kind := "func"
				if recv != "" {
					kind = "method"
				}
				sym := &SummarySymbol{
					ID: canonical, ParentID: fileID, Kind: kind,
					ModuleName: pkgRec.sym.ModuleName, PkgPath: p.PkgPath, FilePath: filePath,
					StartLine: pos.Line, EndLine: end.Line, LOC: end.Line - pos.Line + 1,
					Complexity: cyclomatic(d.Body),
					Exported:   ast.IsExported(d.Name.Name) && (recv == "" || ast.IsExported(recv)),
					SigHash:    sigHash, SelfHash: selfHash,
					IsTest: true, IsGenerated: fileRec.sym.IsGenerated,
					Doc: doc, Signature: sig, Directives: directives,
				}
				decls[canonical] = &summaryDecl{
					sym: sym, recvType: recv, typeRefs: map[string]struct{}{},
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					canonical := "sym:" + p.PkgPath + "." + ts.Name.Name
					if decls[canonical] != nil {
						continue
					}
					docs := make([]string, 0, 2)
					if d.Doc != nil {
						docs = append(docs, d.Doc.Text())
					}
					if ts.Doc != nil {
						docs = append(docs, ts.Doc.Text())
					}
					doc := normalizeDoc(strings.Join(docs, "\n"))
					directives := nodeDirectives(file, ts, d.Doc, ts.Doc)
					sig := printNode(ts.Type)
					sigHash := hashText(sig)
					selfHash := hashParts(sigHash, hashText(doc), hashText(strings.Join(directives, "\n")))
					pos := fset.Position(ts.Pos())
					end := fset.Position(ts.End())
					sym := &SummarySymbol{
						ID: canonical, ParentID: fileID, Kind: "type",
						ModuleName: pkgRec.sym.ModuleName, PkgPath: p.PkgPath, FilePath: filePath,
						StartLine: pos.Line, EndLine: end.Line, LOC: end.Line - pos.Line + 1,
						Exported: ast.IsExported(ts.Name.Name),
						SigHash:  sigHash, SelfHash: selfHash,
						IsTest: true, IsGenerated: fileRec.sym.IsGenerated,
						Doc: doc, Signature: sig, Directives: directives,
					}
					decls[canonical] = &summaryDecl{
						sym: sym, isType: true, typeRefs: map[string]struct{}{},
					}
				}
			}
		}
	}
}

func astRecvType(d *ast.FuncDecl) (string, bool) {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return "", false
	}
	expr := d.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		return astRecvName(star.X), true
	}
	return astRecvName(expr), false
}

func astRecvName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.ParenExpr:
		return astRecvName(e.X)
	case *ast.IndexExpr:
		return astRecvName(e.X)
	case *ast.IndexListExpr:
		return astRecvName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

func testCanonicalID(pkgPath, recv string, ptr bool, name string) string {
	if recv == "" {
		return "sym:" + pkgPath + "." + name
	}
	if ptr {
		recv = "*" + recv
	}
	return "sym:" + pkgPath + ".(" + recv + ")." + name
}

package index

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func newSummaryBuild(b *builder) *summaryBuild {
	return &summaryBuild{
		b:              b,
		data:           &SummaryData{ByID: map[string]*SummarySymbol{}},
		modules:        map[string]*summaryModule{},
		packagesByPath: map[string]*summaryPackage{},
		files:          map[string]*summaryFile{},
		decls:          map[string]*summaryDecl{},
		typesByID:      map[string]*summaryType{},
		forced:         map[string]bool{},
		byExisting:     map[string]string{},
		methodsByType:  map[string][]string{},
	}
}

func (s *summaryBuild) add(sym *SummarySymbol) {
	s.data.Symbols = append(s.data.Symbols, sym)
	s.data.ByID[sym.ID] = sym
}

func (b *builder) buildSummary(pkgs, testPkgs []*packages.Package) {
	s := newSummaryBuild(b)
	s.addModules()
	s.addPackages()
	s.collectPackages(pkgs, groupTestSyntax(testPkgs))
	s.groupMethods()
	s.classifyDeclarations()
	s.computeHashes()
	s.rollupFiles()
	s.rollupPackages()
	s.rollupModules()
	s.buildEdges()
	sort.Slice(s.data.Symbols, func(i, j int) bool { return s.data.Symbols[i].ID < s.data.Symbols[j].ID })
	b.idx.Summary = s.data
}

// testSyntaxFile is one already-parsed *_test.go file from a test package
// variant. Reusing these ASTs avoids a second disk read/parse and naturally
// honors the build tags the loader applied.
type testSyntaxFile struct {
	file *ast.File
	fset *token.FileSet
}

func groupTestSyntax(testPkgs []*packages.Package) map[string][]testSyntaxFile {
	grouped := map[string][]testSyntaxFile{}
	seen := map[string]bool{}
	for _, p := range testPkgs {
		if p == nil || p.ForTest == "" || p.Fset == nil {
			continue
		}
		for _, file := range p.Syntax {
			if file == nil {
				continue
			}
			path := p.Fset.Position(file.Pos()).Filename
			if !strings.HasSuffix(path, "_test.go") || seen[path] {
				continue
			}
			seen[path] = true
			grouped[p.ForTest] = append(grouped[p.ForTest], testSyntaxFile{file: file, fset: p.Fset})
		}
	}
	return grouped
}

func (s *summaryBuild) addModules() {
	for _, name := range sortedKeys(s.b.idx.Modules) {
		mod := s.b.idx.Modules[name]
		sym := &SummarySymbol{
			ID: "mod:" + name, Kind: "module", ModuleName: name,
			LOC: mod.ProdLOC, Eligible: true,
		}
		s.add(sym)
		s.modules[name] = &summaryModule{sym: sym}
	}
}

func (s *summaryBuild) addPackages() {
	for _, pkgPath := range sortedKeys(s.b.idx.Packages) {
		pkg := s.b.idx.Packages[pkgPath]
		sym := &SummarySymbol{
			ID: "pkg:" + pkgPath, ParentID: "mod:" + pkg.ModuleArea,
			Kind: "package", ModuleName: pkg.ModuleArea, PkgPath: pkgPath,
			LOC: pkg.ProdLOC, Eligible: true,
		}
		s.add(sym)
		rec := &summaryPackage{sym: sym, docs: map[string]string{}}
		s.packagesByPath[pkgPath] = rec
		if mod := s.modules[pkg.ModuleArea]; mod != nil {
			mod.packages = append(mod.packages, rec)
		}
	}
}

func (s *summaryBuild) collectPackages(pkgs []*packages.Package, testFiles map[string][]testSyntaxFile) {
	for existingID, sym := range s.b.idx.Symbols {
		if canonical := sym.CanonicalID(); canonical != "" {
			s.byExisting[existingID] = canonical
		}
	}
	for _, p := range pkgs {
		s.collectPackage(p, testFiles[p.PkgPath])
	}
}

func (s *summaryBuild) collectPackage(p *packages.Package, testFiles []testSyntaxFile) {
	pkgRec := s.packagesByPath[p.PkgPath]
	if pkgRec == nil || p.TypesInfo == nil || p.Fset == nil {
		return
	}
	for _, file := range p.Syntax {
		s.collectFile(p, file, pkgRec)
	}
	s.b.addTestFileSummaries(p, pkgRec, testFiles, s.data, s.files, s.decls, s.forced)
}

func (s *summaryBuild) collectFile(p *packages.Package, file *ast.File, pkgRec *summaryPackage) {
	if file == nil {
		return
	}
	absPath := p.Fset.Position(file.Pos()).Filename
	filePath := relFile(s.b.absRoot, absPath)
	fileID := "file:" + filePath
	fileRec := s.files[fileID]
	if fileRec == nil {
		fileRec = s.addFile(p, pkgRec, file, absPath, filePath, fileID)
	}
	if file.Doc != nil {
		pkgRec.docs[filePath] = normalizeDoc(file.Doc.Text())
	}
	for _, decl := range file.Decls {
		s.collectDecl(p, file, decl, fileRec)
	}
}

func (s *summaryBuild) addFile(p *packages.Package, pkgRec *summaryPackage, file *ast.File, absPath, filePath, fileID string) *summaryFile {
	loc := 0
	generated := false
	if facts, err := s.b.factsFor(absPath); err == nil {
		loc = facts.loc
		generated = facts.generated
	}
	dirs := directiveLines(file.Comments)
	sym := &SummarySymbol{
		ID: fileID, ParentID: pkgRec.sym.ID, Kind: "file",
		ModuleName: pkgRec.sym.ModuleName, PkgPath: p.PkgPath,
		FilePath: filePath, StartLine: 1, EndLine: loc, LOC: loc,
		IsTest: strings.HasSuffix(filePath, "_test.go"), IsGenerated: generated,
		SelfHash:   hashText(strings.Join(dirs, "\n")),
		Directives: dirs,
	}
	s.add(sym)
	fileRec := &summaryFile{sym: sym}
	s.files[fileID] = fileRec
	pkgRec.files = append(pkgRec.files, fileRec)
	if generated {
		s.forced[fileID] = true
	} else {
		pkgRec.productiveFiles++
	}
	return fileRec
}

func (s *summaryBuild) collectDecl(p *packages.Package, file *ast.File, decl ast.Decl, fileRec *summaryFile) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		s.b.summaryFunc(p, file, d, fileRec, s.decls)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				s.b.summaryType(p, file, d, ts, fileRec, s.decls, s.typesByID)
			}
		}
	}
}

func (s *summaryBuild) groupMethods() {
	for _, decl := range s.decls {
		s.data.Symbols = append(s.data.Symbols, decl.sym)
		s.data.ByID[decl.sym.ID] = decl.sym
		if decl.sym.Kind != "method" {
			continue
		}
		typeExistingID := decl.sym.PkgPath + "." + decl.recvType
		s.methodsByType[typeExistingID] = append(s.methodsByType[typeExistingID], decl.sym.ID)
	}
}

func (s *summaryBuild) classifyDeclarations() {
	dead := map[string]bool{}
	for _, sym := range s.b.idx.DeadFunctions("", true) {
		dead[sym.ID] = true
	}
	for _, decl := range s.decls {
		s.classifyDecl(decl, dead)
	}
	s.applyForcedIneligibility()
	s.classifyFiles()
	s.classifyPackages()
	s.classifyModules()
}

func (s *summaryBuild) classifyDecl(decl *summaryDecl, dead map[string]bool) {
	sym := decl.sym
	sym.FanIn = len(s.b.idx.Callers[decl.existingID])
	sym.Dead = dead[decl.existingID]
	if decl.isType {
		sym.FanIn = len(s.b.idx.XRefs[decl.existingID]) + len(s.b.idx.TypeImpls[decl.existingID])
		shape := s.typesByID[sym.ID]
		if shape != nil {
			methodCount := len(s.methodsByType[shape.existingID])
			sym.Eligible = sym.Exported && (shape.isInterface || methodCount >= typeEligibleMethods || shape.structFields >= typeEligibleFields)
		}
		return
	}
	sym.Eligible = sym.LOC >= funcEligibleLOC || sym.Complexity >= funcEligibleComplexity || sym.FanIn >= funcEligibleFanIn
}

func (s *summaryBuild) applyForcedIneligibility() {
	for _, decl := range s.decls {
		if s.forced[decl.sym.ParentID] {
			decl.sym.Eligible = false
		}
	}
}

func (s *summaryBuild) classifyFiles() {
	eligibleByFile := map[string]int{}
	for _, decl := range s.decls {
		if decl.sym.Eligible {
			eligibleByFile[decl.sym.ParentID]++
		}
	}
	for id, fileRec := range s.files {
		if s.forced[id] {
			fileRec.sym.Eligible = false
			continue
		}
		fileRec.sym.Eligible = eligibleByFile[id] >= fileEligibleSymbols && fileRec.sym.LOC >= fileEligibleLOC
	}
}

func (s *summaryBuild) classifyPackages() {
	for _, pkgRec := range s.packagesByPath {
		pkgRec.sym.Eligible = pkgRec.productiveFiles > 0
	}
}

func (s *summaryBuild) classifyModules() {
	for _, modRec := range s.modules {
		// A module is an architectural unit regardless of how many packages it
		// contains. Single-package modules (the norm for internal/<area>,
		// cmd/<app>, and modules/<name> layouts) must still be summarizable.
		// Test-only and generated-only modules remain ineligible.
		modRec.sym.Eligible = false
		for _, pkgRec := range modRec.packages {
			if pkgRec.sym.Eligible {
				modRec.sym.Eligible = true
				break
			}
		}
	}
}

func (s *summaryBuild) computeHashes() {
	sigByID := map[string]string{}
	for id, decl := range s.decls {
		sigByID[id] = decl.sym.SigHash
	}
	for _, decl := range s.decls {
		decl.sym.DepHash = s.declarationDepHash(decl, sigByID)
		decl.sym.ContentHash = contentHash(decl.sym.SelfHash, decl.sym.DepHash)
	}
}

func (s *summaryBuild) declarationDepHash(decl *summaryDecl, sigByID map[string]string) string {
	lines := make([]string, 0)
	if decl.isType {
		for _, methodID := range s.methodsByType[decl.existingID] {
			if sig := sigByID[methodID]; sig != "" {
				lines = append(lines, "method:"+methodID+":"+sig)
			}
		}
	} else {
		for _, callee := range decl.calleeIDs {
			calleeID := s.byExisting[callee]
			if sig := sigByID[calleeID]; sig != "" {
				lines = append(lines, "callee:"+calleeID+":"+sig)
			}
		}
	}
	for ref := range decl.typeRefs {
		if sig := sigByID[ref]; sig != "" {
			lines = append(lines, "type:"+ref+":"+sig)
		}
	}
	return hashSorted(lines)
}

func (s *summaryBuild) rollupFiles() {
	childrenByFile := map[string][]*SummarySymbol{}
	for _, decl := range s.decls {
		childrenByFile[decl.sym.ParentID] = append(childrenByFile[decl.sym.ParentID], decl.sym)
	}
	for id, fileRec := range s.files {
		lines := make([]string, 0, len(childrenByFile[id]))
		for _, child := range childrenByFile[id] {
			lines = append(lines, "child:"+child.ID+":"+child.ContentHash)
		}
		fileRec.sym.DepHash = hashSorted(lines)
		fileRec.sym.ContentHash = contentHash(fileRec.sym.SelfHash, fileRec.sym.DepHash)
	}
}

func (s *summaryBuild) rollupPackages() {
	for _, pkgRec := range s.packagesByPath {
		s.rollupPackage(pkgRec)
	}
}

func (s *summaryBuild) rollupPackage(pkgRec *summaryPackage) {
	docLines := make([]string, 0, len(pkgRec.docs))
	docTexts := make([]string, 0, len(pkgRec.docs))
	for _, name := range sortedKeys(pkgRec.docs) {
		doc := pkgRec.docs[name]
		if doc == "" {
			continue
		}
		docLines = append(docLines, name+"\n"+doc)
		docTexts = append(docTexts, doc)
	}
	pkgRec.sym.Doc = strings.Join(docTexts, "\n")
	pkgRec.sym.SelfHash = hashText(strings.Join(docLines, "\n"))
	lines := make([]string, 0, len(pkgRec.files))
	for _, fileRec := range pkgRec.files {
		if strings.HasSuffix(fileRec.sym.FilePath, "_test.go") {
			continue
		}
		lines = append(lines, "file:"+fileRec.sym.ID+":"+fileRec.sym.ContentHash)
	}
	pkgRec.sym.DepHash = hashSorted(lines)
	pkgRec.sym.ContentHash = contentHash(pkgRec.sym.SelfHash, pkgRec.sym.DepHash)
}

func (s *summaryBuild) rollupModules() {
	for _, modRec := range s.modules {
		lines := make([]string, 0, len(modRec.packages))
		for _, pkgRec := range modRec.packages {
			lines = append(lines, "pkg:"+pkgRec.sym.ID+":"+pkgRec.sym.ContentHash)
		}
		modRec.sym.DepHash = hashSorted(lines)
		modRec.sym.ContentHash = contentHash(modRec.sym.SelfHash, modRec.sym.DepHash)
	}
}

func (s *summaryBuild) buildEdges() {
	edgeSet := map[SummaryEdge]struct{}{}
	addEdge := s.edgeAdder(edgeSet)
	s.addCallEdges(addEdge)
	s.addImplementationEdges(addEdge)
	s.addEmbeddingEdges(addEdge)
	s.addImportEdges(addEdge)
	for edge := range edgeSet {
		s.data.Edges = append(s.data.Edges, edge)
	}
	sort.Slice(s.data.Edges, func(i, j int) bool {
		if s.data.Edges[i].SrcID != s.data.Edges[j].SrcID {
			return s.data.Edges[i].SrcID < s.data.Edges[j].SrcID
		}
		if s.data.Edges[i].Kind != s.data.Edges[j].Kind {
			return s.data.Edges[i].Kind < s.data.Edges[j].Kind
		}
		return s.data.Edges[i].DstID < s.data.Edges[j].DstID
	})
}

func (s *summaryBuild) edgeAdder(edgeSet map[SummaryEdge]struct{}) func(src, dst, kind string) {
	return func(src, dst, kind string) {
		if src == "" || dst == "" || src == dst {
			return
		}
		if s.data.ByID[src] == nil || s.data.ByID[dst] == nil {
			return
		}
		edgeSet[SummaryEdge{SrcID: src, DstID: dst, Kind: kind}] = struct{}{}
	}
}

func (s *summaryBuild) addCallEdges(addEdge func(src, dst, kind string)) {
	for src, callees := range s.b.idx.Callees {
		for _, dst := range callees {
			addEdge(s.byExisting[src], s.byExisting[dst], "calls")
		}
	}
}

func (s *summaryBuild) addImplementationEdges(addEdge func(src, dst, kind string)) {
	for ifaceID, matches := range s.b.idx.IfaceImpls {
		for _, match := range matches {
			addEdge(s.byExisting[match.TypeID], s.byExisting[ifaceID], "implements")
		}
	}
}

func (s *summaryBuild) addEmbeddingEdges(addEdge func(src, dst, kind string)) {
	for _, shape := range s.typesByID {
		for _, embedded := range shape.embeddedTypes {
			addEdge(shape.sym.ID, embedded, "embeds")
		}
	}
}

func (s *summaryBuild) addImportEdges(addEdge func(src, dst, kind string)) {
	for pkgPath, pkg := range s.b.idx.Packages {
		for _, imported := range pkg.Imports {
			addEdge("pkg:"+pkgPath, "pkg:"+imported, "imports")
		}
	}
}

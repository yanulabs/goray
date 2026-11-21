package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	funcEligibleLOC        = 60
	funcEligibleComplexity = 10
	funcEligibleFanIn      = 8
	typeEligibleMethods    = 3
	typeEligibleFields     = 6
	fileEligibleSymbols    = 3
	fileEligibleLOC        = 250
)

// SummaryData is the transient semantic snapshot used by the summary cache.
type SummaryData struct {
	Symbols []*SummarySymbol
	ByID    map[string]*SummarySymbol
	Edges   []SummaryEdge
}

// SummarySymbol is one row of current working-tree state.
type SummarySymbol struct {
	ID          string
	ParentID    string
	Kind        string
	ModuleName  string
	PkgPath     string
	FilePath    string
	StartLine   int
	EndLine     int
	LOC         int
	Complexity  int
	FanIn       int
	Exported    bool
	Dead        bool
	Eligible    bool
	IsTest      bool
	IsGenerated bool
	SigHash     string
	SelfHash    string
	DepHash     string
	ContentHash string

	Doc        string
	Signature  string
	Directives []string
}

type SummaryEdge struct {
	SrcID string
	DstID string
	Kind  string
}

func (d *SummaryData) Get(id string) *SummarySymbol {
	if d == nil {
		return nil
	}
	return d.ByID[id]
}

// CanonicalID is a line-independent ID used by the summary cache.
func (s *Symbol) CanonicalID() string {
	switch s.Kind {
	case "func":
		return "sym:" + s.PkgPath + "." + s.Name
	case "method":
		recv := s.RecvType
		if s.RecvPtr {
			recv = "*" + recv
		}
		return "sym:" + s.PkgPath + ".(" + recv + ")." + s.Name
	case "type", "interface", "struct":
		return "sym:" + s.PkgPath + "." + s.Name
	default:
		return ""
	}
}

type summaryDecl struct {
	sym        *SummarySymbol
	existingID string
	calleeIDs  []string
	typeRefs   map[string]struct{}
	recvType   string
	isType     bool
}

type summaryType struct {
	sym           *SummarySymbol
	existingID    string
	structFields  int
	isInterface   bool
	embeddedTypes []string
}

type summaryFile struct {
	sym *SummarySymbol
}

type summaryPackage struct {
	sym             *SummarySymbol
	files           []*summaryFile
	docs            map[string]string
	productiveFiles int
}

type summaryModule struct {
	sym      *SummarySymbol
	packages []*summaryPackage
}

type summaryBuild struct {
	b              *builder
	data           *SummaryData
	modules        map[string]*summaryModule
	packagesByPath map[string]*summaryPackage
	files          map[string]*summaryFile
	decls          map[string]*summaryDecl
	typesByID      map[string]*summaryType
	forced         map[string]bool
	byExisting     map[string]string
	methodsByType  map[string][]string
}

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

func (b *builder) buildSummary(pkgs []*packages.Package) {
	s := newSummaryBuild(b)
	s.addModules()
	s.addPackages()
	s.collectPackages(pkgs)
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

func (s *summaryBuild) collectPackages(pkgs []*packages.Package) {
	for existingID, sym := range s.b.idx.Symbols {
		if canonical := sym.CanonicalID(); canonical != "" {
			s.byExisting[existingID] = canonical
		}
	}
	for _, p := range pkgs {
		s.collectPackage(p)
	}
}

func (s *summaryBuild) collectPackage(p *packages.Package) {
	pkgRec := s.packagesByPath[p.PkgPath]
	if pkgRec == nil || p.TypesInfo == nil || p.Fset == nil {
		return
	}
	for _, file := range p.Syntax {
		s.collectFile(p, file, pkgRec)
	}
	s.b.addTestFileSummaries(p, pkgRec, s.data, s.files, s.decls, s.forced)
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
	if src, err := os.ReadFile(absPath); err == nil {
		loc = countLines(src)
		generated = isGenerated(filepath.Base(absPath), src)
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
		modRec.sym.Eligible = len(modRec.packages) >= 2
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
	if d.Doc != nil {
		doc = d.Doc.Text()
	}
	directives := nodeDirectives(file, d, d.Doc)
	body := ""
	if d.Body != nil {
		body = printNode(d.Body)
	}
	sigInput := printNode(d.Type) + "\n" + b.canonicalType(fn.Type())
	sigHash := hashText(sigInput)
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
		Doc: doc, Signature: sym.Signature, Directives: directives,
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
	if gen.Doc != nil {
		docs = append(docs, gen.Doc.Text())
	}
	if spec.Doc != nil {
		docs = append(docs, spec.Doc.Text())
	}
	doc := normalizeDoc(strings.Join(docs, "\n"))
	directives := nodeDirectives(file, spec, gen.Doc, spec.Doc)
	sigInput := printNode(spec.Type) + "\n" + b.canonicalType(tn.Type())
	sigHash := hashText(sigInput)
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
		Doc: doc, Signature: sym.Signature, Directives: directives,
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

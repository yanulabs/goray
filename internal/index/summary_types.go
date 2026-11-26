package index

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

// SummaryEdge is one dependency edge in the summary graph.
type SummaryEdge struct {
	SrcID string
	DstID string
	Kind  string
}

// Get returns a summary symbol by canonical ID, or nil when absent.
func (d *SummaryData) Get(id string) *SummarySymbol {
	if d == nil {
		return nil
	}
	return d.ByID[id]
}

// CanonicalID is a line-independent ID used by the summary cache.
func (s *Symbol) CanonicalID() string {
	if s.canonicalID != "" {
		return s.canonicalID
	}
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

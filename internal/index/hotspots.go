package index

import "sort"

// HotspotOptions controls hotspot ranking and filtering.
type HotspotOptions struct {
	Target           string
	SortBy           string
	Limit            int
	Module           string
	ExcludeModules   []string
	MinComplexity    int
	MinLoc           int
	IncludeGenerated bool
	TrackedOnly      bool
	UntestedOnly     bool
}

func (opts HotspotOptions) selected(module string, generated, untracked bool) bool {
	if opts.Module != "" && module != opts.Module {
		return false
	}
	for _, excluded := range opts.ExcludeModules {
		if excluded != "" && module == excluded {
			return false
		}
	}
	if !opts.IncludeGenerated && generated {
		return false
	}
	if opts.TrackedOnly && untracked {
		return false
	}
	return true
}

func (opts HotspotOptions) meets(complexity, loc int) bool {
	return complexity >= opts.MinComplexity && loc >= opts.MinLoc
}

func (opts HotspotOptions) allowed(module string, generated, untracked bool, complexity, loc int) bool {
	return opts.selected(module, generated, untracked) && opts.meets(complexity, loc)
}

// Hotspot is one ranked item. TestCovered is a pointer so an explicit false is
// serialized for function hotspots.
type Hotspot struct {
	ID              string `json:"id"`
	CanonicalID     string `json:"canonical_id,omitempty"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	File            string `json:"file"`
	Line            int    `json:"line"`
	Module          string `json:"module"`
	LOC             int    `json:"loc"`
	Complexity      int    `json:"complexity"`
	TotalComplexity int    `json:"total_complexity,omitempty"`
	FanIn           int    `json:"fan_in"`
	RiskScore       int    `json:"risk_score"`
	TestCovered     *bool  `json:"test_covered,omitempty"`
	Fields          int    `json:"fields,omitempty"`
	Methods         int    `json:"methods,omitempty"`
	Generated       bool   `json:"generated,omitempty"`
	Untracked       bool   `json:"untracked,omitempty"`
}

// Hotspots returns ranked items and the total number of matches before limit.
func (idx *Index) Hotspots(opts HotspotOptions) ([]Hotspot, int) {
	var items []Hotspot
	switch opts.Target {
	case "func":
		items = idx.funcHotspots(opts)
	case "file":
		items = idx.fileHotspots(opts)
	case "type":
		items = idx.typeHotspots(opts)
	default:
		items = idx.funcHotspots(opts)
	}
	sortHotspots(items, opts.SortBy)
	total := len(items)
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items, total
}

func (idx *Index) funcHotspots(opts HotspotOptions) []Hotspot {
	items := make([]Hotspot, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "func" && sym.Kind != "method" {
			continue
		}
		if sym.IsTest || !opts.allowed(sym.ModuleArea, sym.Generated, sym.Untracked, sym.Cyclomatic, sym.LOC) {
			continue
		}
		covered := idx.TestCovered[sym.ID]
		if opts.UntestedOnly && covered {
			continue
		}
		fanIn := len(idx.Callers[sym.ID])
		items = append(items, Hotspot{
			ID: sym.ID, CanonicalID: sym.CanonicalID(), Name: sym.Name, Kind: sym.Kind,
			File: sym.File, Line: sym.Line, Module: sym.ModuleArea,
			LOC: sym.LOC, Complexity: sym.Cyclomatic, FanIn: fanIn,
			RiskScore: sym.Cyclomatic*2 + fanIn, TestCovered: &covered,
			Generated: sym.Generated, Untracked: sym.Untracked,
		})
	}
	return items
}

type fileAggregate struct {
	hotspot   Hotspot
	totalComp int
	callers   map[string]bool
}

func (idx *Index) fileHotspots(opts HotspotOptions) []Hotspot {
	aggregates := map[string]*fileAggregate{}
	for _, sym := range idx.Symbols {
		// Filters that depend on file-level aggregates (min_complexity,
		// min_loc) are applied after aggregation so a large file is not
		// discarded because its individual symbols are small.
		if !opts.selected(sym.ModuleArea, sym.Generated, sym.Untracked) {
			continue
		}
		agg := aggregates[sym.File]
		if agg == nil {
			agg = &fileAggregate{
				hotspot: Hotspot{
					ID: "file:" + sym.File, CanonicalID: "file:" + sym.File,
					Name: sym.File, Kind: "file", File: sym.File, Module: sym.ModuleArea,
					Generated: sym.Generated, Untracked: sym.Untracked,
				},
				callers: map[string]bool{},
			}
			aggregates[sym.File] = agg
		}
		if sym.LOC > 0 {
			agg.hotspot.LOC += sym.LOC
		}
		agg.totalComp += sym.Cyclomatic
		if sym.Cyclomatic > agg.hotspot.Complexity {
			agg.hotspot.Complexity = sym.Cyclomatic
		}
		if sym.Line > 0 && (agg.hotspot.Line == 0 || sym.Line < agg.hotspot.Line) {
			agg.hotspot.Line = sym.Line
		}
		agg.hotspot.Generated = agg.hotspot.Generated || sym.Generated
		agg.hotspot.Untracked = agg.hotspot.Untracked || sym.Untracked
		for _, caller := range idx.Callers[sym.ID] {
			agg.callers[caller] = true
		}
	}
	items := make([]Hotspot, 0, len(aggregates))
	for path, agg := range aggregates {
		for _, metric := range idx.LargeFiles {
			if metric.Path == path {
				agg.hotspot.LOC = metric.LOC
				break
			}
		}
		agg.hotspot.FanIn = len(agg.callers)
		agg.hotspot.TotalComplexity = agg.totalComp
		agg.hotspot.RiskScore = agg.hotspot.Complexity*3 + agg.hotspot.LOC/10 + agg.hotspot.FanIn*2
		if !opts.meets(agg.hotspot.Complexity, agg.hotspot.LOC) {
			continue
		}
		items = append(items, agg.hotspot)
	}
	return items
}

func (idx *Index) typeHotspots(opts HotspotOptions) []Hotspot {
	methodCounts := map[string]int{}
	for _, sym := range idx.Symbols {
		if sym.Kind == "method" && sym.RecvType != "" {
			methodCounts[sym.PkgPath+"."+sym.RecvType]++
		}
	}
	items := make([]Hotspot, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "struct" && sym.Kind != "type" {
			continue
		}
		if !opts.selected(sym.ModuleArea, sym.Generated, sym.Untracked) {
			continue
		}
		fanIn := len(idx.Callers[sym.ID]) + len(idx.XRefs[sym.ID])
		methods := methodCounts[sym.PkgPath+"."+sym.Name]
		complexity := sym.Fields + methods
		if !opts.meets(complexity, sym.LOC) {
			continue
		}
		items = append(items, Hotspot{
			ID: sym.ID, CanonicalID: sym.CanonicalID(), Name: sym.Name, Kind: sym.Kind,
			File: sym.File, Line: sym.Line, Module: sym.ModuleArea, LOC: sym.LOC,
			Complexity: complexity, FanIn: fanIn, RiskScore: sym.Fields*2 + methods*3 + fanIn,
			Fields: sym.Fields, Methods: methods, Generated: sym.Generated, Untracked: sym.Untracked,
		})
	}
	return items
}

func sortHotspots(items []Hotspot, sortBy string) {
	score := func(item Hotspot) int {
		switch sortBy {
		case "complexity":
			return item.Complexity
		case "loc":
			return item.LOC
		case "fan_in":
			return item.FanIn
		default:
			return item.RiskScore
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if score(items[i]) != score(items[j]) {
			return score(items[i]) > score(items[j])
		}
		return items[i].ID < items[j].ID
	})
}

package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

const maxDiffHotspotDeltas = 10

// Gate names accepted by --fail-on and get_architecture_diff.
const (
	GateCycles           = "cycles"
	GateDeadCode         = "dead_code"
	GateBreakingAPI      = "breaking_api"
	GateUntestedHotspots = "untested_hotspots"
)

// GateNames lists every supported gate in canonical order.
var GateNames = []string{GateCycles, GateDeadCode, GateBreakingAPI, GateUntestedHotspots}

// ValidGate reports whether gate is a supported fail-on gate.
func ValidGate(gate string) bool {
	for _, name := range GateNames {
		if gate == name {
			return true
		}
	}
	return false
}

// NormalizeGates accepts strings and arrays, splitting comma- or
// whitespace-separated values, and validates every gate. Duplicates are
// collapsed so repeated inputs do not duplicate failed_gates.
func NormalizeGates(values []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, gate := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			if gate == "" || seen[gate] {
				continue
			}
			if !ValidGate(gate) {
				return nil, fmt.Errorf("unknown fail_on gate %q (use %s)", gate, strings.Join(GateNames, ", "))
			}
			seen[gate] = true
			out = append(out, gate)
		}
	}
	return out, nil
}

// Compare computes every architectural delta between base and head. The
// returned Report has no ref metadata; BuildContext fills it in.
func Compare(base, head *index.Index) Report {
	return Report{
		Summary: Counters{
			Packages: len(head.Packages), PackagesBase: len(base.Packages),
			Files: indexFileCount(head), FilesBase: indexFileCount(base),
			LOC: indexLOC(head), LOCBase: indexLOC(base),
			Symbols: len(head.Symbols), SymbolsBase: len(base.Symbols),
			ModuleEdges: moduleEdgeCount(head), ModuleEdgesBase: moduleEdgeCount(base),
			UntestedHotspots: untestedHotspotCount(head),
		},
		Cycles:   compareCycles(base, head),
		API:      compareAPI(base, head),
		Hotspots: compareHotspots(base, head),
		Dead:     compareDeadCode(base, head),
	}
}

func compareCycles(base, head *index.Index) Cycles {
	var out Cycles
	baseCycles := cycleSet(base)
	headCycles := cycleSet(head)
	for cycle := range headCycles {
		if !baseCycles[cycle] {
			out.New = append(out.New, cycle)
		}
	}
	for cycle := range baseCycles {
		if !headCycles[cycle] {
			out.Resolved = append(out.Resolved, cycle)
		}
	}
	sort.Strings(out.New)
	sort.Strings(out.Resolved)
	return out
}

func compareAPI(base, head *index.Index) API {
	var out API
	baseAPI := exportedAPI(base)
	headAPI := exportedAPI(head)
	for id, after := range headAPI {
		before, ok := baseAPI[id]
		if !ok {
			out.Added = append(out.Added, after)
			continue
		}
		if before.Before != after.Before {
			changed := after
			changed.After = after.Before
			changed.Before = before.Before
			out.Modified = append(out.Modified, changed)
		}
	}
	for id, before := range baseAPI {
		if _, ok := headAPI[id]; !ok {
			out.Removed = append(out.Removed, before)
		}
	}
	sortAPIChanges(out.Added)
	sortAPIChanges(out.Modified)
	sortAPIChanges(out.Removed)
	return out
}

func compareHotspots(base, head *index.Index) Hotspots {
	var out Hotspots
	headHotspots := complexFunctions(head)
	baseHotspots := complexFunctions(base)
	for id, after := range headHotspots {
		if head.TestCovered[id] {
			continue
		}
		before, existed := baseHotspots[id]
		if !existed {
			out.New = append(out.New, after)
			continue
		}
		deltaCC := after.AfterCC - before.AfterCC
		deltaLOC := after.AfterLOC - before.AfterLOC
		// Improvements (lower CC/LOC with tests still present) must pass the
		// gate. Regressions and newly lost test coverage must not.
		regressed := deltaCC > 0 || deltaLOC > 0
		coverageLost := before.TestCovered && !head.TestCovered[id]
		if !regressed && !coverageLost {
			continue
		}
		delta := after
		delta.BeforeCC = before.AfterCC
		delta.BeforeLOC = before.AfterLOC
		delta.DeltaCC = deltaCC
		delta.DeltaLOC = deltaLOC
		delta.CoverageLost = coverageLost
		out.TopDelta = append(out.TopDelta, delta)
	}
	sort.Slice(out.New, func(i, j int) bool {
		if out.New[i].AfterCC != out.New[j].AfterCC {
			return out.New[i].AfterCC > out.New[j].AfterCC
		}
		return out.New[i].ID < out.New[j].ID
	})
	sort.Slice(out.TopDelta, func(i, j int) bool {
		if out.TopDelta[i].CoverageLost != out.TopDelta[j].CoverageLost {
			return out.TopDelta[i].CoverageLost
		}
		left := out.TopDelta[i].DeltaCC*1000 + out.TopDelta[i].DeltaLOC
		right := out.TopDelta[j].DeltaCC*1000 + out.TopDelta[j].DeltaLOC
		if left != right {
			return left > right
		}
		return out.TopDelta[i].ID < out.TopDelta[j].ID
	})
	if len(out.TopDelta) > maxDiffHotspotDeltas {
		out.TopDelta = out.TopDelta[:maxDiffHotspotDeltas]
	}
	return out
}

func compareDeadCode(base, head *index.Index) DeadCode {
	var out DeadCode
	deadOpts := index.DeadCodeOptions{}
	baseDead := deadSet(base.DeadCode(deadOpts))
	headDead := deadSet(head.DeadCode(deadOpts))
	for id, item := range headDead {
		if _, ok := baseDead[id]; !ok {
			out.New = append(out.New, item)
		}
	}
	for id, item := range baseDead {
		if _, ok := headDead[id]; !ok {
			out.Removed = append(out.Removed, item)
		}
	}
	sortDeadItems(out.New)
	sortDeadItems(out.Removed)
	return out
}

func cycleSet(idx *index.Index) map[string]bool {
	out := map[string]bool{}
	for _, cycle := range idx.ModuleCycles() {
		out[strings.Join(cycle, " -> ")] = true
	}
	return out
}

func exportedAPI(idx *index.Index) map[string]APIChange {
	out := map[string]APIChange{}
	for id, sym := range idx.Symbols {
		if sym.IsTest || !sym.Exported || index.IsClosedWorld(sym.PkgRel) {
			continue
		}
		switch sym.Kind {
		case "func", "method", "type", "interface", "struct", "var", "const", "field":
		default:
			continue
		}
		if sym.Kind == "field" {
			// An exported field of an unexported type is not externally
			// reachable, so it is not part of the breaking API.
			owner, ok := idx.Symbols[sym.PkgPath+"."+sym.RecvType]
			if !ok || !owner.Exported {
				continue
			}
		}
		canonical := sym.CanonicalID()
		if canonical == "" {
			canonical = id
		}
		name := sym.Name
		if sym.Kind == "field" && sym.RecvType != "" {
			name = sym.RecvType + "." + sym.Name
		}
		out[canonical] = APIChange{
			ID: canonical, Name: name, Kind: sym.Kind, File: sym.File, Line: sym.Line,
			Before: sym.Signature,
		}
	}
	return out
}

func complexFunctions(idx *index.Index) map[string]Hotspot {
	out := map[string]Hotspot{}
	for id, sym := range idx.Symbols {
		if sym.IsTest || (sym.Kind != "func" && sym.Kind != "method") || sym.Cyclomatic < 10 {
			continue
		}
		out[id] = Hotspot{
			ID: id, Name: sym.Name, File: sym.File, Line: sym.Line, Module: sym.ModuleArea,
			AfterCC: sym.Cyclomatic, AfterLOC: sym.LOC, TestCovered: idx.TestCovered[id],
		}
	}
	return out
}

func untestedHotspotCount(idx *index.Index) int {
	count := 0
	for id, sym := range idx.Symbols {
		if sym.IsTest || (sym.Kind != "func" && sym.Kind != "method") || sym.Cyclomatic < 10 {
			continue
		}
		if !idx.TestCovered[id] {
			count++
		}
	}
	return count
}

func deadSet(items []*index.Symbol) map[string]DeadItem {
	out := map[string]DeadItem{}
	for _, sym := range items {
		out[sym.ID] = DeadItem{
			ID: sym.ID, Name: sym.Name, Kind: sym.Kind, Module: sym.ModuleArea,
			File: sym.File, Line: sym.Line,
		}
	}
	return out
}

func indexFileCount(idx *index.Index) int {
	n := 0
	for _, pkg := range idx.Packages {
		n += pkg.Files + pkg.TestFiles
	}
	return n
}

func indexLOC(idx *index.Index) int {
	n := 0
	for _, pkg := range idx.Packages {
		n += pkg.ProdLOC + pkg.TestLOC
	}
	return n
}

func moduleEdgeCount(idx *index.Index) int {
	n := 0
	for _, mod := range idx.Modules {
		n += len(mod.OutModules)
	}
	return n
}

func sortAPIChanges(items []APIChange) {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

func sortDeadItems(items []DeadItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

// GateTriggered reports whether a gate should fail for the given report.
func GateTriggered(report Report, gate string) bool {
	switch gate {
	case GateCycles:
		return len(report.Cycles.New) > 0
	case GateDeadCode:
		return len(report.Dead.New) > 0
	case GateBreakingAPI:
		return len(report.API.Removed) > 0 || len(report.API.Modified) > 0
	case GateUntestedHotspots:
		return len(report.Hotspots.New) > 0 || len(report.Hotspots.TopDelta) > 0
	}
	return false
}

// FailedGates evaluates gates in order and returns the triggered ones.
func FailedGates(report Report, gates []string) []string {
	var failed []string
	for _, gate := range gates {
		if GateTriggered(report, gate) {
			failed = append(failed, gate)
		}
	}
	return failed
}

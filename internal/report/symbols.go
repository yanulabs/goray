package report

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

type reportSymbols struct {
	rows                []render.HTMLSymbol
	cached              map[string]cache.Cached
	canonical           map[string]*index.Symbol
	canonicalByExisting map[string]string
}

func loadReportSymbols(ctx context.Context, idx *index.Index, store *cache.Cache) (reportSymbols, error) {
	canonical, canonicalByExisting := canonicalSymbols(idx)
	cached, err := loadCachedSummaries(ctx, idx, store)
	if err != nil {
		return reportSymbols{}, err
	}
	return reportSymbols{
		rows:                buildSymbolRows(idx, cached, canonical, canonicalByExisting),
		cached:              cached,
		canonical:           canonical,
		canonicalByExisting: canonicalByExisting,
	}, nil
}

func canonicalSymbols(idx *index.Index) (map[string]*index.Symbol, map[string]string) {
	canonical := make(map[string]*index.Symbol)
	canonicalByExisting := make(map[string]string)
	for _, sym := range idx.Symbols {
		id := sym.CanonicalID()
		if id == "" {
			continue
		}
		canonical[id] = sym
		canonicalByExisting[sym.ID] = id
	}
	return canonical, canonicalByExisting
}

func loadCachedSummaries(ctx context.Context, idx *index.Index, store *cache.Cache) (map[string]cache.Cached, error) {
	cached := map[string]cache.Cached{}
	if store == nil {
		return cached, nil
	}
	for _, sym := range idx.Summary.Symbols {
		item, err := store.Lookup(ctx, sym.ID)
		if err != nil {
			return nil, err
		}
		cached[sym.ID] = item
	}
	return cached, nil
}

func buildSymbolRows(idx *index.Index, cached map[string]cache.Cached, canonical map[string]*index.Symbol, canonicalByExisting map[string]string) []render.HTMLSymbol {
	rows := make([]render.HTMLSymbol, 0, len(idx.Summary.Symbols))
	for _, summary := range idx.Summary.Symbols {
		rows = append(rows, buildSymbolRow(idx, summary, cached, canonical, canonicalByExisting))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func buildSymbolRow(idx *index.Index, summary *index.SummarySymbol, cached map[string]cache.Cached, canonical map[string]*index.Symbol, canonicalByExisting map[string]string) render.HTMLSymbol {
	sym := canonical[summary.ID]
	row := render.HTMLSymbol{
		ID: summary.ID, Name: symbolLabel(summary, sym), Kind: summary.Kind,
		PkgPath: summary.PkgPath, Module: summary.ModuleName, File: summary.FilePath,
		StartLine: summary.StartLine, EndLine: summary.EndLine,
		LOC: summary.LOC, Complexity: summary.Complexity, FanIn: summary.FanIn,
		FanOut: fanOut(idx, summary, sym), Exported: summary.Exported,
		Dead: summary.Dead, Eligible: summary.Eligible,
		IsTest: summary.IsTest, IsGenerated: summary.IsGenerated,
		Untracked:   untrackedFor(idx, summary, sym),
		TestCovered: testCoveredFor(idx, sym),
		SyncState:   stateFor(cached, summary),
		Signature:   signatureFor(summary, sym),
		Doc:         docFor(summary),
		Summary:     summaryView(cached[summary.ID]),
	}
	if sym != nil && (sym.Kind == "func" || sym.Kind == "method") {
		row.Callers = canonicalRefs(idx.Callers[sym.ID], canonicalByExisting)
		row.Callees = canonicalRefs(idx.Callees[sym.ID], canonicalByExisting)
	}
	return row
}

func untrackedFor(idx *index.Index, summary *index.SummarySymbol, sym *index.Symbol) bool {
	if sym != nil {
		return sym.Untracked
	}
	return idx.UntrackedFiles[summary.FilePath]
}

func testCoveredFor(idx *index.Index, sym *index.Symbol) *bool {
	if sym == nil || (sym.Kind != "func" && sym.Kind != "method") {
		return nil
	}
	covered := idx.TestCovered[sym.ID]
	return &covered
}

func signatureFor(summary *index.SummarySymbol, sym *index.Symbol) string {
	if summary.Signature != "" {
		return summary.Signature
	}
	if sym != nil {
		return sym.Signature
	}
	return ""
}

func docFor(summary *index.SummarySymbol) string {
	return summary.Doc
}

func symbolLabel(summary *index.SummarySymbol, sym *index.Symbol) string {
	if sym != nil {
		return sym.Name
	}
	switch summary.Kind {
	case "module":
		return summary.ModuleName
	case "package":
		return summary.PkgPath
	case "file":
		return summary.FilePath
	}
	if i := strings.LastIndex(summary.ID, "."); i >= 0 {
		return summary.ID[i+1:]
	}
	return summary.ID
}

func fanOut(idx *index.Index, summary *index.SummarySymbol, sym *index.Symbol) int {
	if sym != nil {
		switch sym.Kind {
		case "func", "method":
			return len(idx.Callees[sym.ID])
		case "type", "interface", "struct":
			return len(idx.TypeImpls[sym.ID])
		}
	}
	switch summary.Kind {
	case "module":
		if mod := idx.Modules[summary.ModuleName]; mod != nil {
			return len(mod.OutModules)
		}
	case "package":
		if pkg := idx.Packages[summary.PkgPath]; pkg != nil {
			return len(pkg.Imports)
		}
	}
	return 0
}

func canonicalRefs(ids []string, canonicalByExisting map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if canonical := canonicalByExisting[id]; canonical != "" {
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}

func stateFor(cached map[string]cache.Cached, summary *index.SummarySymbol) string {
	if item, ok := cached[summary.ID]; ok && item.State != "" {
		return string(item.State)
	}
	if !summary.Eligible {
		return string(cache.StateNotEligible)
	}
	return string(cache.StateMissing)
}

func summaryView(item cache.Cached) *render.HTMLSummary {
	if len(item.Summary) == 0 {
		return nil
	}
	var payload struct {
		Intent        string   `json:"intent"`
		SideEffects   []string `json:"side_effects"`
		Invariants    []string `json:"invariants"`
		Concurrency   string   `json:"concurrency"`
		KeyInvariants []string `json:"key_invariants"`
		Boundaries    []string `json:"boundaries"`
	}
	if err := json.Unmarshal(item.Summary, &payload); err != nil {
		return nil
	}
	return &render.HTMLSummary{
		Intent: payload.Intent, SideEffects: payload.SideEffects,
		Invariants: payload.Invariants, Concurrency: payload.Concurrency,
		KeyInvariants: payload.KeyInvariants, Boundaries: payload.Boundaries,
		SyncState: string(item.State), ModelID: item.ModelID, DepsChanged: item.DepsChanged,
	}
}

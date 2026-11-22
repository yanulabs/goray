package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/yanulabs/goray/internal/index"
)

// SummaryContext gathers source, schema, staleness, and dependency summaries.
func (c *Cache) SummaryContext(ctx context.Context, id string, idx *index.Index) (SummaryContext, error) {
	sym, err := c.workingSymbol(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SummaryContext{}, fmt.Errorf("symbol not found: %s", id)
		}
		return SummaryContext{}, err
	}
	state := c.classify(ctx, sym)
	cached, cachedHash, err := c.cachedForState(ctx, sym, state)
	if err != nil {
		return SummaryContext{}, err
	}

	out := SummaryContext{
		Symbol:          *sym,
		SyncState:       state,
		PromptVersion:   PromptVersion,
		Schema:          SummarySchema(sym.Kind),
		SelfHash:        sym.SelfHash,
		DepHash:         sym.DepHash,
		ContentHash:     sym.ContentHash,
		PreviousSummary: cached,
	}
	if cachedHash != "" {
		c.touch(cachedHash)
	}
	if idx != nil {
		if meta := idx.Summary.Get(id); meta != nil {
			out.Doc = meta.Doc
			out.Signature = meta.Signature
			out.Directives = meta.Directives
		}
	}
	if state == StateNotEligible {
		out.Note = "symbol is not eligible for summarization; use the parent package/module summary or raw source"
		out.Source = readSource(idx, sym)
		c.populateSummaryParents(ctx, sym, &out)
		return out, nil
	}
	if state != StateSynced && state != StateNotEligible {
		out.Reason, out.DepsChanged = c.staleReason(ctx, sym, state)
	}
	if sym.Kind == "func" || sym.Kind == "method" || sym.Kind == "type" {
		out.Source = readSource(idx, sym)
		out.Dependencies, err = c.dependencies(ctx, sym, idx, true)
		if err != nil {
			return SummaryContext{}, err
		}
	} else {
		out.Dependencies, err = c.children(ctx, sym, idx, true)
		if err != nil {
			return SummaryContext{}, err
		}
	}
	if sym.Kind == "module" {
		c.populateModuleContext(sym, idx, &out)
	}
	if state == StateSoftStale || state == StateHardStale {
		for _, dep := range out.Dependencies {
			if dep.SyncState != StateSynced {
				out.ChangedDependencies = append(out.ChangedDependencies, dep.ID)
			}
		}
	}
	return out, nil
}

func (c *Cache) populateSummaryParents(ctx context.Context, sym *WorkingSymbol, out *SummaryContext) {
	if sym.PkgPath != "" {
		out.ParentPackageID = "pkg:" + sym.PkgPath
	}
	if sym.ModuleName != "" {
		out.ParentModuleID = "mod:" + sym.ModuleName
	}
	stateFor := func(id string) (bool, SyncState) {
		if id == "" {
			return false, ""
		}
		parent, err := c.workingSymbol(ctx, id)
		if err != nil {
			return false, ""
		}
		state := c.classify(ctx, parent)
		cached, _, err := c.cachedForState(ctx, parent, state)
		if err != nil {
			return false, state
		}
		return len(cached) > 0, state
	}
	if cached, state := stateFor(out.ParentPackageID); state != "" {
		out.ParentPackageSummaryCached = &cached
		out.ParentPackageSyncState = state
	}
	if cached, state := stateFor(out.ParentModuleID); state != "" {
		out.ParentModuleSummaryCached = &cached
		out.ParentModuleSyncState = state
	}
}

const (
	moduleAPILimit        = 40
	moduleKeyFileLimit    = 3
	moduleKeyFileMaxRunes = 1200
	modulePackageDocRunes = 300
)

// populateModuleContext adds compact package rollups, module edges, exported
// API signatures, key file excerpts, and cached child package summary IDs for
// mod: targets. The payload is bounded so a direct get_summary_context call
// stays small enough to feed into upsert_summaries without extra probing.
func (c *Cache) populateModuleContext(sym *WorkingSymbol, idx *index.Index, out *SummaryContext) {
	if sym == nil || idx == nil {
		return
	}
	mod := idx.Modules[sym.ModuleName]
	if mod == nil {
		return
	}
	ctx := ModuleContext{
		Name: sym.ModuleName, ProdLOC: mod.ProdLOC, TestLOC: mod.TestLOC,
		Packages: mod.Packages, Files: mod.Files,
		DependencyEdges: ModuleEdges{Out: moduleEdgeList(mod.OutModules), In: moduleEdgeList(mod.InModules)},
	}
	c.appendModulePackages(idx, sym.ModuleName, &ctx)
	api, files := moduleSymbolSamples(idx, sym.ModuleName)
	ctx.ExportedAPI, ctx.Truncated = limitModuleAPI(api, ctx.Truncated)
	keyFiles, keyTruncated := moduleKeyFiles(idx, files)
	ctx.KeyFiles = keyFiles
	ctx.Truncated = ctx.Truncated || keyTruncated
	for _, dep := range out.Dependencies {
		if dep.Kind == "package" && len(dep.Summary) > 0 {
			ctx.CachedPackageSummaries = append(ctx.CachedPackageSummaries, dep.ID)
		}
	}
	out.Module = &ctx
}

func (c *Cache) appendModulePackages(idx *index.Index, module string, ctx *ModuleContext) {
	for _, pkg := range idx.Packages {
		if pkg.ModuleArea != module {
			continue
		}
		rollup := ModulePackage{
			ID: "pkg:" + pkg.PkgPath, PkgPath: pkg.PkgPath, RelPath: pkg.RelPath, Name: pkg.Name,
			ProdFiles: pkg.ProdFiles, Files: pkg.Files, ProdLOC: pkg.ProdLOC, TestLOC: pkg.TestLOC,
			Exported: pkg.Exported, TestOnly: pkg.TestOnly, Imports: pkg.Imports,
		}
		if meta := idx.Summary.Get("pkg:" + pkg.PkgPath); meta != nil {
			if doc, cut := truncateModuleRunes(meta.Doc, modulePackageDocRunes); doc != "" {
				rollup.Doc = doc
				if cut {
					rollup.Doc += "..."
				}
			}
		}
		ctx.PackageRollups = append(ctx.PackageRollups, rollup)
	}
	sort.Slice(ctx.PackageRollups, func(i, j int) bool {
		if ctx.PackageRollups[i].RelPath != ctx.PackageRollups[j].RelPath {
			return ctx.PackageRollups[i].RelPath < ctx.PackageRollups[j].RelPath
		}
		return ctx.PackageRollups[i].ID < ctx.PackageRollups[j].ID
	})
}

func moduleSymbolSamples(idx *index.Index, module string) ([]ModuleAPI, []*index.SummarySymbol) {
	api := make([]ModuleAPI, 0)
	files := make([]*index.SummarySymbol, 0)
	if idx.Summary == nil {
		return api, files
	}
	for _, meta := range idx.Summary.Symbols {
		if meta.ModuleName != module {
			continue
		}
		switch meta.Kind {
		case "module", "package":
			continue
		case "file":
			if !meta.IsTest && !meta.IsGenerated && meta.LOC > 0 {
				files = append(files, meta)
			}
			continue
		}
		if !meta.Exported || meta.IsTest || meta.IsGenerated {
			continue
		}
		api = append(api, ModuleAPI{
			ID: meta.ID, Kind: meta.Kind, PkgPath: meta.PkgPath,
			Signature: meta.Signature, FilePath: meta.FilePath, StartLine: meta.StartLine,
		})
	}
	return api, files
}

func limitModuleAPI(api []ModuleAPI, truncated bool) ([]ModuleAPI, bool) {
	sort.Slice(api, func(i, j int) bool {
		if api[i].PkgPath != api[j].PkgPath {
			return api[i].PkgPath < api[j].PkgPath
		}
		return api[i].ID < api[j].ID
	})
	if len(api) <= moduleAPILimit {
		return api, truncated
	}
	return api[:moduleAPILimit], true
}

func moduleKeyFiles(idx *index.Index, files []*index.SummarySymbol) ([]ModuleFileExcerpt, bool) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].LOC != files[j].LOC {
			return files[i].LOC > files[j].LOC
		}
		return files[i].FilePath < files[j].FilePath
	})
	truncated := len(files) > moduleKeyFileLimit
	if truncated {
		files = files[:moduleKeyFileLimit]
	}
	out := make([]ModuleFileExcerpt, 0, len(files))
	for _, file := range files {
		excerpt := readSource(idx, &WorkingSymbol{FilePath: file.FilePath, StartLine: 1, EndLine: file.EndLine})
		if excerpt == "" {
			continue
		}
		text, cut := truncateModuleRunes(excerpt, moduleKeyFileMaxRunes)
		if cut {
			text += "..."
			truncated = true
		}
		out = append(out, ModuleFileExcerpt{Path: file.FilePath, LOC: file.LOC, Excerpt: text, Truncated: cut})
	}
	return out, truncated
}

func moduleEdgeList(edges map[string][]string) []ModuleEdge {
	keys := make([]string, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ModuleEdge, 0, len(keys))
	for _, key := range keys {
		out = append(out, ModuleEdge{Module: key, Symbols: len(edges[key])})
	}
	return out
}

func truncateModuleRunes(text string, limit int) (string, bool) {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:limit]), true
}

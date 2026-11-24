// Package report assembles the offline HTML architecture report.
package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

// Build combines an immutable index snapshot with cached summaries. It syncs
// the working tables first and is intended for one-shot CLI exports.
func Build(ctx context.Context, idx *index.Index, store *cache.Cache) (render.HTMLReport, error) {
	return build(ctx, idx, store, true)
}

// BuildFromCache assembles a report without rewriting working tables. The
// caller must guarantee that store already contains the snapshot (for example
// through Store.SetBuildHook).
func BuildFromCache(ctx context.Context, idx *index.Index, store *cache.Cache) (render.HTMLReport, error) {
	return build(ctx, idx, store, false)
}

func build(ctx context.Context, idx *index.Index, store *cache.Cache, syncIndex bool) (render.HTMLReport, error) {
	if idx == nil || idx.Summary == nil {
		return render.HTMLReport{}, errors.New("index has no summary metadata")
	}
	if syncIndex && store != nil {
		if err := store.Sync(ctx, idx); err != nil {
			return render.HTMLReport{}, fmt.Errorf("sync summary cache: %w", err)
		}
	}

	symbols, err := loadReportSymbols(ctx, idx, store)
	if err != nil {
		return render.HTMLReport{}, err
	}
	metrics := buildMetrics(idx, symbols.rows)
	coverage, err := buildCoverage(ctx, store)
	if err != nil {
		return render.HTMLReport{}, err
	}
	cycles := idx.ModuleCycles()
	if cycles == nil {
		cycles = [][]string{}
	}

	return render.HTMLReport{
		Repo: idx.RootDir, ModulePath: idx.ModulePath,
		ModulePaths: idx.ModulePaths, BuildTags: idx.BuildTags, Workspace: idx.Workspace,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Metrics:     metrics, Coverage: coverage,
		Hotspots:        render.BuildHotspots(symbols.rows),
		CoupledPackages: buildCoupling(idx),
		Modules:         buildModuleCards(idx, symbols.cached),
		Packages:        buildPackageCards(idx, symbols.cached),
		Symbols:         symbols.rows,
		Interfaces:      buildInterfaces(idx, symbols.canonicalByExisting),
		Graphs: render.HTMLGraphs{
			Modules:  render.LayoutGraph(buildModuleGraph(idx, symbols.cached)),
			Packages: render.LayoutGraph(buildPackageGraph(idx, symbols.cached)),
		},
		Tree:         buildTree(idx, symbols.cached, symbols.canonical),
		ModuleCycles: cycles,
	}, nil
}

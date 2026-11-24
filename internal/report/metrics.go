package report

import (
	"context"
	"sort"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func buildMetrics(idx *index.Index, rows []render.HTMLSymbol) render.HTMLMetrics {
	metrics := render.HTMLMetrics{Modules: len(idx.Modules), Packages: len(idx.Packages)}
	for _, pkg := range idx.Packages {
		metrics.LOC += pkg.ProdLOC
	}
	for _, row := range rows {
		switch row.Kind {
		case "file":
			metrics.Files++
		case "type":
			metrics.Types++
		case "func":
			metrics.Funcs++
		case "method":
			metrics.Methods++
		}
		if row.Dead {
			metrics.Dead++
		}
	}
	return metrics
}

func buildCoverage(ctx context.Context, store *cache.Cache) (render.HTMLCoverage, error) {
	if store == nil {
		return render.HTMLCoverage{}, nil
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		return render.HTMLCoverage{}, err
	}
	return coverageFromStats(stats), nil
}

func coverageFromStats(stats cache.Stats) render.HTMLCoverage {
	states := stats.SummaryStates
	out := render.HTMLCoverage{
		Eligible: stats.EligibleSymbols, Synced: states["synced"],
		SoftStale: states["soft_stale"], HardStale: states["hard_stale"],
		Missing: states["missing"], NotEligible: stats.Tables["working_symbols"] - stats.EligibleSymbols,
	}
	if out.Eligible > 0 {
		out.Percent = float64(out.Synced) / float64(out.Eligible) * 100
		out.Percent = float64(int(out.Percent*10+0.5)) / 10
	}
	return out
}

func buildCoupling(idx *index.Index) []render.HTMLCoupled {
	out := make([]render.HTMLCoupled, 0, len(idx.Packages))
	for pkgPath, pkg := range idx.Packages {
		out = append(out, render.HTMLCoupled{
			Pkg: pkgPath, FanIn: len(idx.RevImports[pkgPath]), FanOut: len(pkg.Imports),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].FanIn+out[i].FanOut, out[j].FanIn+out[j].FanOut
		if left != right {
			return left > right
		}
		return out[i].Pkg < out[j].Pkg
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

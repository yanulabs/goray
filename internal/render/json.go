package render

import (
	"sort"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

type jsonRenderer struct{ baseRenderer }

func newJSON() jsonRenderer {
	return jsonRenderer{baseRenderer{name: "json"}}
}

func (jsonRenderer) RenderCodebaseMap(idx *index.Index) string {
	complex := make([]*index.Symbol, 0)
	for _, sym := range idx.Symbols {
		if sym.Cyclomatic >= 10 {
			complex = append(complex, sym)
		}
	}
	sort.Slice(complex, func(i, j int) bool {
		if complex[i].Cyclomatic != complex[j].Cyclomatic {
			return complex[i].Cyclomatic > complex[j].Cyclomatic
		}
		return complex[i].ID < complex[j].ID
	})
	complex = limit(complex, 20)

	large := append([]index.FileMetric{}, idx.LargeFiles...)
	large = limit(large, 25)

	return JSON(map[string]any{
		"module_path": idx.ModulePath,
		"packages":    len(idx.Packages),
		"symbols":     len(idx.Symbols),
		"modules":     idx.ModulesByLOC(),
		"large_files": large,
		"top_complex": complex,
	})
}

func (jsonRenderer) RenderModule(idx *index.Index, mod *index.ModSummary) string {
	view := struct {
		*index.ModSummary
		Subpackages []*index.PkgInfo `json:"subpackages"`
	}{
		ModSummary:  mod,
		Subpackages: modulePackages(idx, mod.Name),
	}
	return JSON(view)
}

func (jsonRenderer) RenderGraph(graph index.Graph) string {
	return JSON(graph)
}

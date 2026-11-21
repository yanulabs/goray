package render

import (
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

type jsonRenderer struct{ baseRenderer }

func newJSON() jsonRenderer {
	return jsonRenderer{baseRenderer{name: "json"}}
}

func (jsonRenderer) RenderCodebaseMap(idx *index.Index) string {
	hotspots := codebaseHotspots(idx, 10)
	large := make([]index.FileMetric, 0, len(idx.LargeFiles))
	for _, file := range idx.LargeFiles {
		if !file.Generated {
			large = append(large, file)
		}
	}
	large = limit(large, 10)

	cycles := idx.ModuleCycles()
	cycleStrings := make([]string, 0, len(cycles))
	for _, cycle := range cycles {
		cycleStrings = append(cycleStrings, strings.Join(cycle, " -> "))
	}

	return JSON(map[string]any{
		"module_path":       idx.ModulePath,
		"packages":          len(idx.Packages),
		"symbols":           len(idx.Symbols),
		"generated_files":   generatedFileCount(idx),
		"untracked_files":   untrackedFileCount(idx),
		"modules":           idx.ModulesByLOC(),
		"cross_module_edges": moduleEdges(idx),
		"module_cycles":     cycleStrings,
		"large_files":       large,
		"top_complex":       hotspots,
		"top_hotspots":      hotspots,
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

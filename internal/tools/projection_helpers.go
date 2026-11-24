package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yanulabs/goray/internal/index"
)

func compactModuleSummary(mod *index.ModSummary) compactModule {
	return compactModule{
		Name: mod.Name, Packages: mod.Packages, Files: mod.Files,
		GeneratedFiles: mod.GeneratedFiles, GeneratedSymbols: mod.GeneratedSymbols,
		GeneratedLOC: mod.GeneratedLOC, UntrackedFiles: mod.UntrackedFiles,
		ProdLOC: mod.ProdLOC, TestLOC: mod.TestLOC, TestOnly: mod.TestOnly,
		AllUntracked: mod.AllUntracked, RootExported: mod.RootExported,
		PortsCalls: mod.PortsCalls, Goroutines: mod.Goroutines,
		FanIn: len(mod.InModules), FanOut: len(mod.OutModules),
	}
}

func sortedModuleKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func generatedTotals(idx *index.Index) (files, symbols, loc int) {
	for _, pkg := range idx.Packages {
		files += pkg.GeneratedFiles
		symbols += pkg.GeneratedSymbols
		loc += pkg.GeneratedLOC
	}
	return files, symbols, loc
}

func paginateCompactModules(modules []compactModule, topModules int) ([]compactModule, int) {
	if topModules > 0 && len(modules) > topModules {
		return modules[:topModules], len(modules) - topModules
	}
	return modules, 0
}

func pageLargeFiles(files []index.FileMetric, includeGenerated bool) ([]index.FileMetric, int) {
	filtered := make([]index.FileMetric, 0, len(files))
	for _, file := range files {
		if includeGenerated || !file.Generated {
			filtered = append(filtered, file)
		}
	}
	total := len(filtered)
	return limitFiles(filtered, 10), total
}

func writePayloadFooter(b *strings.Builder, truncated bool, total int, hint string) {
	fmt.Fprintf(b, "\n## Payload\n\n- truncated: %t\n- total_count: %d\n", truncated, total)
	if hint != "" {
		fmt.Fprintf(b, "- hint: %s\n", hint)
	}
}

func limitFiles(items []index.FileMetric, limit int) []index.FileMetric {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

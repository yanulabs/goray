package render

import (
	"sort"
	"strings"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

func safeID(s string) string {
	return strings.NewReplacer("-", "_", "/", "_", ".", "_").Replace(s)
}

func limit[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

func modulePackages(idx *index.Index, name string) []*index.PkgInfo {
	packages := make([]*index.PkgInfo, 0)
	for _, pkg := range idx.Packages {
		if pkg.ModuleArea == name {
			packages = append(packages, pkg)
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].RelPath < packages[j].RelPath })
	return packages
}

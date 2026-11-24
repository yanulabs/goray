package index

import (
	"sort"
	"strings"
)

// BuildOptions controls package loading.
type BuildOptions struct {
	// Tags is the normalized set of Go build tags passed to the loader.
	Tags []string
	// Workspace enables multi-module go.work indexing. When false, a root
	// directory that contains go.work but no go.mod is auto-detected.
	Workspace bool
	// ReleaseArtifacts drops type-checked package artifacts after the index is
	// built. Long-running servers should enable this to avoid retaining every
	// AST and go/types graph; subsequent watched changes use a full rebuild.
	ReleaseArtifacts bool
}

// NormalizeTags splits comma- or space-separated tags, trims, deduplicates,
// and sorts them so equivalent tag sets produce identical cache keys.
func NormalizeTags(raw []string) []string {
	seen := map[string]struct{}{}
	for _, value := range raw {
		for _, tag := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			seen[tag] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

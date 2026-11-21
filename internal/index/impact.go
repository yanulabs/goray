package index

import (
	"sort"
	"strings"
)

// Impact is the blast radius of a change.
type Impact struct {
	Risk             string   `json:"risk"`
	Modules          []string `json:"impacted_modules"`
	Packages         int      `json:"impacted_pkgs"`
	Files            []string `json:"impacted_files"`
	Callers          []string `json:"impacted_callers"`
	TestPackages     []string `json:"test_packages"`
	SuggestedCommand string   `json:"suggested_cmd"`
}

// ImpactBySymbol follows references and transitive callers.
func (idx *Index) ImpactBySymbol(sym *Symbol, depth int) Impact {
	pkgs := map[string]struct{}{sym.PkgPath: {}}
	files := map[string]struct{}{sym.File: {}}
	mods := map[string]struct{}{}
	callers := map[string]struct{}{}
	if sym.ModuleArea != "" {
		mods[sym.ModuleArea] = struct{}{}
	}

	for _, ref := range idx.XRefs[sym.ID] {
		pkgs[ref.FromPkg] = struct{}{}
		files[ref.File] = struct{}{}
		if ref.FromMod != "" {
			mods[ref.FromMod] = struct{}{}
		}
		if ref.FromFuncID != "" {
			callers[ref.FromFuncID] = struct{}{}
		}
	}

	seen := map[string]int{sym.ID: 0}
	queue := []string{sym.ID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] >= depth {
			continue
		}
		for _, caller := range idx.Callers[cur] {
			if _, ok := seen[caller]; ok {
				continue
			}
			seen[caller] = seen[cur] + 1
			queue = append(queue, caller)
			callers[caller] = struct{}{}
			if cs, ok := idx.Symbols[caller]; ok {
				pkgs[cs.PkgPath] = struct{}{}
				files[cs.File] = struct{}{}
				if cs.ModuleArea != "" {
					mods[cs.ModuleArea] = struct{}{}
				}
			}
		}
	}
	return idx.impact(pkgs, files, mods, callers)
}

// ImpactByPackage follows reverse imports.
func (idx *Index) ImpactByPackage(pkgPath string, depth int) Impact {
	pkgs := map[string]struct{}{pkgPath: {}}
	mods := map[string]struct{}{}
	if pi, ok := idx.Packages[pkgPath]; ok && pi.ModuleArea != "" {
		mods[pi.ModuleArea] = struct{}{}
	}

	seen := map[string]int{pkgPath: 0}
	queue := []string{pkgPath}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] >= depth {
			continue
		}
		for _, prev := range idx.RevImports[cur] {
			if _, ok := seen[prev]; ok {
				continue
			}
			seen[prev] = seen[cur] + 1
			queue = append(queue, prev)
			pkgs[prev] = struct{}{}
			if pi, ok := idx.Packages[prev]; ok && pi.ModuleArea != "" {
				mods[pi.ModuleArea] = struct{}{}
			}
		}
	}
	return idx.impact(pkgs, nil, mods, nil)
}

func (idx *Index) impact(pkgs, files, mods, callers map[string]struct{}) Impact {
	testPkgs := make([]string, 0)
	for pkg := range pkgs {
		pi, ok := idx.Packages[pkg]
		if !ok || pi.TestFiles == 0 {
			continue
		}
		if pi.RelPath == "." {
			testPkgs = append(testPkgs, ".")
		} else {
			testPkgs = append(testPkgs, "./"+pi.RelPath)
		}
	}
	sort.Strings(testPkgs)

	risk := "low"
	switch {
	case len(mods) >= 4 || len(pkgs) >= 10:
		risk = "high"
	case len(mods) >= 2 || len(pkgs) >= 4:
		risk = "medium"
	}

	return Impact{
		Risk:             risk,
		Modules:          sortedKeys(mods),
		Packages:         len(pkgs),
		Files:            sortedKeys(files),
		Callers:          sortedKeys(callers),
		TestPackages:     testPkgs,
		SuggestedCommand: strings.TrimSpace("go test -race -count=1 " + strings.Join(testPkgs, " ")),
	}
}

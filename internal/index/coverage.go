package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// computeTestCoverage derives test reachability without executing go test. It
// seeds from direct callees of test functions and walks the indexed call graph.
// Name-based seeding is intentionally conservative: over-marking a symbol as
// covered is preferable to reporting live production code as untested.
func (b *builder) computeTestCoverage() {
	seeds := map[string]bool{}
	_ = filepath.WalkDir(b.absRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := relFile(b.absRoot, path)
		if strings.HasPrefix(rel, "testdata/") || strings.Contains(rel, "/testdata/") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isTestEntry(fn.Name.Name) || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				id := calledIdent(call.Fun)
				if id == nil {
					return true
				}
				for _, sym := range b.idx.SymbolsByName[strings.ToLower(id.Name)] {
					if !sym.IsTest && (sym.Kind == "func" || sym.Kind == "method") {
						seeds[sym.ID] = true
					}
				}
				return true
			})
		}
		return nil
	})

	covered := make(map[string]bool, len(seeds))
	queue := make([]string, 0, len(seeds))
	for id := range seeds {
		covered[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, callee := range b.idx.Callees[cur] {
			if covered[callee] {
				continue
			}
			covered[callee] = true
			queue = append(queue, callee)
		}
	}
	for id := range covered {
		if sym := b.idx.Symbols[id]; sym != nil && !sym.IsTest {
			b.idx.TestCovered[id] = true
		}
	}
}

func isTestEntry(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

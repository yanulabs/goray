package render

import (
	"strings"
	"testing"

	"github.com/yanulabs/go-mcp-server/internal/index"
)

func TestCodebaseMap(t *testing.T) {
	idx := &index.Index{
		ModulePath: "example.com/app",
		Packages:   map[string]*index.PkgInfo{"a": {}, "b": {}},
		Symbols:    map[string]*index.Symbol{"a.F": {ID: "a.F"}},
		Modules: map[string]*index.ModSummary{
			"api": {
				Name: "api", ProdLOC: 5000, RootExported: 90, PortsCalls: 2,
				OutModules: map[string][]string{"db": {"Open"}},
			},
			"db": {Name: "db"},
		},
		LargeFiles: []index.FileMetric{{Path: "big.go", LOC: 500}},
	}

	md := CodebaseMapMarkdown(idx)
	for _, want := range []string{
		"example.com/app",
		"| `api` |",
		"api -->|1 sym| db",
		"`Open`",
		"big.go",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}

	dot := CodebaseMapDOT(idx)
	if !strings.Contains(dot, "api -> db") || !strings.Contains(dot, "5.0k prod") {
		t.Fatalf("unexpected DOT:\n%s", dot)
	}
}

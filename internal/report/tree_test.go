package report

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func TestBuildTreeOrderingAndFallbacks(t *testing.T) {
	idx := &index.Index{Summary: &index.SummaryData{Symbols: []*index.SummarySymbol{
		{ID: "sym:p.Z", ParentID: "file:f", Kind: "func"},
		{ID: "pkg:p", ParentID: "mod:m", Kind: "package", PkgPath: "example.com/p"},
		{ID: "sym:p.T", ParentID: "file:f", Kind: "type"},
		{ID: "file:f", ParentID: "pkg:p", Kind: "file", FilePath: "f.go"},
		{ID: "sym:p.Orphan", ParentID: "file:missing", Kind: "func"},
		{ID: "mod:m", Kind: "module", ModuleName: "m", Eligible: true},
		{ID: "sym:p.A", ParentID: "file:f", Kind: "func"},
		{ID: "var:ignored", ParentID: "file:f", Kind: "var"},
	}}}
	canonical := map[string]*index.Symbol{
		"sym:p.T": {ID: "type", Name: "T", Kind: "interface", PkgPath: "example.com/p"},
	}
	cached := map[string]cache.Cached{"mod:m": {State: cache.StateSynced}}
	roots := buildTree(idx, cached, canonical)
	var got []string
	var walk func([]*render.HTMLTreeNode)
	walk = func(nodes []*render.HTMLTreeNode) {
		for _, node := range nodes {
			got = append(got, node.Kind+":"+node.ID+":"+node.SyncState)
			walk(node.Children)
		}
	}
	walk(roots)
	want := []string{
		"module:mod:m:synced",
		"package:pkg:p:not_eligible",
		"file:file:f:not_eligible",
		"interface:sym:p.T:not_eligible",
		"func:sym:p.A:not_eligible",
		"func:sym:p.Z:not_eligible",
		"func:sym:p.Orphan:not_eligible",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree order = %v, want %v", got, want)
	}
}

func TestKindRank(t *testing.T) {
	tests := map[string]int{
		"module": 0, "package": 1, "file": 2,
		"type": 3, "struct": 3, "interface": 3,
		"func": 4, "method": 4, "unknown": 5,
	}
	for kind, want := range tests {
		if got := kindRank(kind); got != want {
			t.Errorf("kindRank(%q) = %d, want %d", kind, got, want)
		}
	}
	if empty := sortedKeys(map[string]int(nil)); empty == nil || len(empty) != 0 {
		t.Fatalf("empty sortedKeys = %#v", empty)
	}
	if got := strings.Join(sortedKeys(map[string]int{"z": 1, "a": 2}), ","); got != "a,z" {
		t.Fatalf("sortedKeys order = %q", got)
	}
}

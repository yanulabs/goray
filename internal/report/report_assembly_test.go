package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
)

func TestBuildRequiresSummaryMetadata(t *testing.T) {
	builders := []struct {
		name  string
		build func(*index.Index) error
	}{
		{"Build", func(idx *index.Index) error {
			_, err := Build(context.Background(), idx, nil)
			return err
		}},
		{"BuildFromCache", func(idx *index.Index) error {
			_, err := BuildFromCache(context.Background(), idx, nil)
			return err
		}},
	}
	indices := []struct {
		name string
		idx  *index.Index
	}{
		{"nil index", nil},
		{"nil summary", &index.Index{}},
	}
	for _, builder := range builders {
		for _, tc := range indices {
			t.Run(builder.name+"/"+tc.name, func(t *testing.T) {
				err := builder.build(tc.idx)
				if err == nil || err.Error() != "index has no summary metadata" {
					t.Fatalf("error = %v", err)
				}
			})
		}
	}
}

func TestBuildFromCachePropagatesCacheReadFailures(t *testing.T) {
	tests := []struct {
		name    string
		symbols []*index.SummarySymbol
	}{
		{"lookup", []*index.SummarySymbol{{ID: "sym:example.com/p.F"}}},
		{"stats", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := cache.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			idx := &index.Index{Summary: &index.SummaryData{Symbols: tc.symbols}}
			if _, err := BuildFromCache(context.Background(), idx, store); err == nil {
				t.Fatal("expected cache read failure")
			}
		})
	}
}

func TestBuildFromCacheUsesBulkSummaryLookup(t *testing.T) {
	dir := t.TempDir()
	var source strings.Builder
	source.WriteString("package big\n\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&source, "func F%d() int { return %d }\n", i, i)
	}
	for name, body := range map[string]string{
		"go.mod":     "module example.com/big\n\ngo 1.26\n",
		"big/big.go": source.String(),
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Sync(context.Background(), idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Summary.Symbols) < 40 {
		t.Fatalf("summary symbols = %d", len(idx.Summary.Symbols))
	}

	before := store.QueryCount()
	report, err := BuildFromCache(context.Background(), idx, store)
	if err != nil {
		t.Fatal(err)
	}
	if delta := store.QueryCount() - before; delta > 1 {
		t.Fatalf("report build issued %d summary queries for %d symbols, want 1", delta, len(idx.Summary.Symbols))
	}
	if report.ModulePath != "example.com/big" {
		t.Fatalf("report module = %q", report.ModulePath)
	}
}

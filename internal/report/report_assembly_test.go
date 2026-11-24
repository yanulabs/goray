package report

import (
	"context"
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

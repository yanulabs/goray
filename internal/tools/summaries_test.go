package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yanulabs/go-mcp-server/internal/cache"
	"github.com/yanulabs/go-mcp-server/internal/index"
	"github.com/yanulabs/go-mcp-server/internal/render"
)

type batchIndexer struct{}

func (batchIndexer) Wait(context.Context) (*index.Index, error) { return &index.Index{}, nil }
func (batchIndexer) Status() index.Status {
	return index.Status{IndexState: index.StateReady}
}
func (batchIndexer) Root() string   { return "." }
func (batchIndexer) Rebuild() error { return nil }

type batchSummaries struct{ sourceSize int }

func (batchSummaries) Enrich(context.Context, string) (json.RawMessage, cache.SyncState, bool, error) {
	return nil, cache.StateMissing, false, nil
}
func (batchSummaries) ListStale(context.Context, cache.StaleFilter) ([]cache.StaleSymbol, error) {
	return nil, nil
}
func (b batchSummaries) SummaryContext(_ context.Context, id string, _ *index.Index) (cache.SummaryContext, error) {
	return cache.SummaryContext{
		Symbol:    cache.WorkingSymbol{ID: id, Kind: "func"},
		SyncState: cache.StateMissing,
		Source:    strings.Repeat("x", b.sourceSize),
		Schema:    cache.SummarySchema("func"),
	}, nil
}
func (batchSummaries) UpsertSummaries(context.Context, []cache.SummaryInput) ([]cache.UpsertResult, error) {
	return nil, nil
}
func (batchSummaries) Coverage(context.Context, string) (cache.Coverage, error) {
	return cache.Coverage{}, nil
}
func (batchSummaries) Path() string { return ":memory:" }

func summaryContextTool(t *testing.T, summaries Summaries) toolFunc {
	t.Helper()
	for _, tool := range New(batchIndexer{}, render.NewRegistry(), summaries) {
		if tool.Name == "get_summary_context" {
			return toolFunc(tool.Run)
		}
	}
	t.Fatal("get_summary_context not found")
	return nil
}

type toolFunc func(context.Context, map[string]any) (string, bool)

func TestSummaryContextBatch(t *testing.T) {
	run := summaryContextTool(t, batchSummaries{sourceSize: 1000})

	text, failed := run(context.Background(), map[string]any{
		"symbol_ids": []any{"sym:a", "sym:b", "sym:c"},
		"max_chars":  float64(1),
	})
	if failed {
		t.Fatal(text)
	}
	var truncated struct {
		Contexts []struct {
			Symbol struct {
				ID string `json:"id"`
			} `json:"symbol"`
		} `json:"contexts"`
		TruncatedIDs []string `json:"truncated_ids"`
	}
	if err := json.Unmarshal([]byte(text), &truncated); err != nil {
		t.Fatal(err)
	}
	if len(truncated.Contexts) != 1 || truncated.Contexts[0].Symbol.ID != "sym:a" {
		t.Fatalf("contexts = %+v", truncated.Contexts)
	}
	if len(truncated.TruncatedIDs) != 2 || truncated.TruncatedIDs[0] != "sym:b" {
		t.Fatalf("truncated = %v", truncated.TruncatedIDs)
	}

	text, failed = run(context.Background(), map[string]any{
		"symbol_ids": []any{"sym:a", "sym:b", "sym:c"},
		"max_chars":  float64(100000),
	})
	if failed {
		t.Fatal(text)
	}
	var full struct {
		Contexts     []json.RawMessage `json:"contexts"`
		TruncatedIDs []string          `json:"truncated_ids"`
	}
	if err := json.Unmarshal([]byte(text), &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Contexts) != 3 || len(full.TruncatedIDs) != 0 {
		t.Fatalf("full batch = %d contexts, truncated=%v", len(full.Contexts), full.TruncatedIDs)
	}

	tooMany := make([]any, 26)
	for i := range tooMany {
		tooMany[i] = "sym:id"
	}
	text, failed = run(context.Background(), map[string]any{
		"symbol_ids": tooMany,
	})
	if !failed {
		t.Fatalf("26 IDs should fail: %s", text)
	}
}

func TestSummaryContextSingleBackwardCompatible(t *testing.T) {
	run := summaryContextTool(t, batchSummaries{sourceSize: 10})
	text, failed := run(context.Background(), map[string]any{"symbol_id": "sym:a"})
	if failed {
		t.Fatal(text)
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(text), &single); err != nil {
		t.Fatal(err)
	}
	if _, ok := single["contexts"]; ok {
		t.Fatal("single-symbol mode must keep its original object shape")
	}
	symbol, ok := single["symbol"].(map[string]any)
	if !ok || symbol["id"] != "sym:a" {
		t.Fatalf("single context = %v", single)
	}
}

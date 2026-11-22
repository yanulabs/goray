package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yanulabs/goray/internal/index"
)

type indexingIndexer struct{}

func (indexingIndexer) Wait(context.Context) (*index.Index, error) {
	return nil, &index.IndexingError{Elapsed: 1500 * time.Millisecond, State: index.StateRebuilding}
}
func (indexingIndexer) Status() index.Status {
	return index.Status{IndexState: index.StateInitializing}
}
func (indexingIndexer) Root() string { return "." }
func (indexingIndexer) RebuildContext(context.Context) error {
	return nil
}

func TestToolsReturnStructuredIndexingState(t *testing.T) {
	localOnly := map[string]bool{
		"project_info": true, "run_tests": true, "run_lint": true, "reindex": true,
		"list_stale_summaries": true, "get_summary_context": true,
		"upsert_summaries": true, "get_summary_coverage": true,
	}
	for _, tool := range New(indexingIndexer{}, nil, fakeSummaries{}) {
		if localOnly[tool.Name] {
			continue // these tools never wait on the index
		}
		text, failed := tool.Run(context.Background(), map[string]any{})
		if failed {
			t.Fatalf("%s should report indexing as a status, not an error: %s", tool.Name, text)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%s returned non-JSON: %q (%v)", tool.Name, text, err)
		}
		if out["index_state"] != "indexing" {
			t.Fatalf("%s index_state = %v", tool.Name, out["index_state"])
		}
		if elapsed, ok := out["elapsed_seconds"].(float64); !ok || elapsed < 1.5 {
			t.Fatalf("%s elapsed_seconds = %v", tool.Name, out["elapsed_seconds"])
		}
		if message, ok := out["message"].(string); !ok || message == "" {
			t.Fatalf("%s message = %v", tool.Name, out["message"])
		}
	}
}

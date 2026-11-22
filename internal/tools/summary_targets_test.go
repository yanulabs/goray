package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func buildSummaryTargetsFixture(t *testing.T) (*index.Index, *cache.Cache) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/summarytargets\n\ngo 1.23\n",
		"modules/solo/solo.go": `package solo

import "example.com/summarytargets/modules/payments/api"

type Service struct{ Name string }

func NewService(name string) *Service { return &Service{Name: name} }

func (s *Service) Run(req api.Request) error { return api.Process(req) }
`,
		"modules/payments/api/api.go": `package api

type Request struct{ ID string }

func Process(req Request) error { return nil }
`,
		"modules/payments/store/store.go": `package store

import "example.com/summarytargets/modules/payments/api"

func Save(req api.Request) error { return api.Process(req) }
`,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Sync(context.Background(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, store
}

func summaryTools(t *testing.T, idx *index.Index, store *cache.Cache) map[string]mcp.Tool {
	t.Helper()
	out := map[string]mcp.Tool{}
	for _, tool := range New(staticIndexer{idx: idx}, render.NewRegistry(), store) {
		out[tool.Name] = tool
	}
	for _, name := range []string{"get_summary_context", "upsert_summaries", "get_module_overview"} {
		if _, ok := out[name]; !ok {
			t.Fatalf("%s not registered", name)
		}
	}
	return out
}

type moduleSummaryContext struct {
	Error   string `json:"error"`
	Input   string `json:"input"`
	Package string `json:"package"`
	Symbol  struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"symbol"`
	SyncState       string          `json:"sync_state"`
	Schema          map[string]any  `json:"schema"`
	SelfHash        string          `json:"self_hash"`
	DepHash         string          `json:"dep_hash"`
	ContentHash     string          `json:"content_hash"`
	PreviousSummary json.RawMessage `json:"previous_summary"`
	Dependencies    []struct {
		ID      string          `json:"id"`
		Kind    string          `json:"kind"`
		Summary json.RawMessage `json:"summary"`
	} `json:"dependencies"`
	ModuleContext struct {
		Name            string            `json:"name"`
		PackageRollups  []json.RawMessage `json:"package_rollups"`
		DependencyEdges struct {
			Out []struct {
				Module string `json:"module"`
			} `json:"out"`
			In []struct {
				Module string `json:"module"`
			} `json:"in"`
		} `json:"dependency_edges"`
		ExportedAPI            []json.RawMessage `json:"exported_api"`
		KeyFiles               []json.RawMessage `json:"key_files"`
		CachedPackageSummaries []string          `json:"cached_package_summaries"`
	} `json:"module_context"`
}

type summaryUpsertResponse struct {
	Results []struct {
		SymbolID string `json:"symbol_id"`
		Status   string `json:"status"`
		Error    string `json:"error"`
	} `json:"results"`
}

func archSummary(intent string) map[string]any {
	return map[string]any{
		"intent":         intent,
		"key_invariants": []string{"invariant-" + intent},
		"boundaries":     []string{"boundary-" + intent},
	}
}

func parseModuleContext(t *testing.T, text string) moduleSummaryContext {
	t.Helper()
	var out moduleSummaryContext
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("invalid summary context JSON: %v\n%s", err, text)
	}
	return out
}

func assertModuleContext(t *testing.T, sc moduleSummaryContext, wantID string) {
	t.Helper()
	if sc.Error != "" {
		t.Fatalf("%s returned error %q (package=%q input=%q)", wantID, sc.Error, sc.Package, sc.Input)
	}
	if sc.Symbol.ID != wantID || sc.Symbol.Kind != "module" {
		t.Fatalf("%s symbol = %+v", wantID, sc.Symbol)
	}
	if sc.DepHash == "" || sc.ContentHash == "" {
		t.Fatalf("%s hashes = self=%q dep=%q content=%q", wantID, sc.SelfHash, sc.DepHash, sc.ContentHash)
	}
	required, _ := sc.Schema["required"].([]any)
	for _, field := range []string{"intent", "key_invariants", "boundaries"} {
		if !slices.Contains(required, any(field)) {
			t.Fatalf("%s schema required = %v", wantID, required)
		}
	}
	if sc.ModuleContext.Name == "" || len(sc.ModuleContext.PackageRollups) == 0 {
		t.Fatalf("%s module context = %+v", wantID, sc.ModuleContext)
	}
	if len(sc.ModuleContext.ExportedAPI) == 0 || len(sc.ModuleContext.KeyFiles) == 0 {
		t.Fatalf("%s api=%d files=%d", wantID, len(sc.ModuleContext.ExportedAPI), len(sc.ModuleContext.KeyFiles))
	}
}

func TestSummaryContextModuleTargetsRoundTrip(t *testing.T) {
	idx, store := buildSummaryTargetsFixture(t)
	tools := summaryTools(t, idx, store)
	getContext := tools["get_summary_context"]
	upsert := tools["upsert_summaries"]

	ids := []string{"mod:payments", "mod:solo"}
	single := map[string]moduleSummaryContext{}
	for _, id := range ids {
		text, failed := getContext.Run(context.Background(), map[string]any{"symbol_id": id})
		if failed {
			t.Fatalf("%s failed: %s", id, text)
		}
		sc := parseModuleContext(t, text)
		assertModuleContext(t, sc, id)
		single[id] = sc
	}
	if len(single["mod:payments"].ModuleContext.PackageRollups) != 2 {
		t.Fatalf("payments packages = %d", len(single["mod:payments"].ModuleContext.PackageRollups))
	}
	if len(single["mod:payments"].ModuleContext.DependencyEdges.In) == 0 {
		t.Fatal("payments should have an incoming module edge from solo")
	}

	items := make([]any, 0, len(ids))
	for _, id := range ids {
		sc := single[id]
		items = append(items, map[string]any{
			"symbol_id": id, "summary": archSummary("first-" + id),
			"expected_self_hash": sc.SelfHash, "expected_dep_hash": sc.DepHash,
			"model_id": "model-v1",
		})
	}
	text, failed := upsert.Run(context.Background(), map[string]any{"items": items})
	if failed {
		t.Fatalf("upsert failed: %s", text)
	}
	var upserted summaryUpsertResponse
	if err := json.Unmarshal([]byte(text), &upserted); err != nil {
		t.Fatal(err)
	}
	if len(upserted.Results) != len(ids) {
		t.Fatalf("results = %+v", upserted.Results)
	}
	for _, result := range upserted.Results {
		if result.Status != "updated" || result.Error != "" {
			t.Fatalf("%s = %+v", result.SymbolID, result)
		}
	}

	// The same IDs in batch mode must resolve identically and return the
	// already-cached child package summaries.
	rawIDs := make([]any, 0, len(ids))
	for _, id := range ids {
		rawIDs = append(rawIDs, id)
	}
	text, failed = getContext.Run(context.Background(), map[string]any{
		"symbol_ids": rawIDs, "max_chars": float64(100000),
	})
	if failed {
		t.Fatalf("batch failed: %s", text)
	}
	var batch struct {
		Contexts     []json.RawMessage `json:"contexts"`
		TruncatedIDs []string          `json:"truncated_ids"`
	}
	if err := json.Unmarshal([]byte(text), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Contexts) != len(ids) || len(batch.TruncatedIDs) != 0 {
		t.Fatalf("batch contexts=%d truncated=%v", len(batch.Contexts), batch.TruncatedIDs)
	}
	batchItems := make([]any, 0, len(ids))
	for i, raw := range batch.Contexts {
		sc := parseModuleContext(t, string(raw))
		assertModuleContext(t, sc, ids[i])
		if sc.SyncState != "synced" || len(sc.PreviousSummary) == 0 {
			t.Fatalf("%s batch state=%s previous=%s", ids[i], sc.SyncState, sc.PreviousSummary)
		}
		batchItems = append(batchItems, map[string]any{
			"symbol_id": ids[i], "summary": archSummary("second-" + ids[i]),
			"expected_self_hash": sc.SelfHash, "expected_dep_hash": sc.DepHash,
			"model_id": "model-v2",
		})
	}
	text, failed = upsert.Run(context.Background(), map[string]any{"items": batchItems})
	if failed {
		t.Fatalf("batch upsert failed: %s", text)
	}
	upserted = summaryUpsertResponse{}
	if err := json.Unmarshal([]byte(text), &upserted); err != nil {
		t.Fatal(err)
	}
	for _, result := range upserted.Results {
		if result.Status != "updated" || result.Error != "" {
			t.Fatalf("%s = %+v", result.SymbolID, result)
		}
	}
}

func TestSummaryContextModuleOverwriteReadBack(t *testing.T) {
	idx, store := buildSummaryTargetsFixture(t)
	tools := summaryTools(t, idx, store)
	getContext := tools["get_summary_context"]
	upsert := tools["upsert_summaries"]
	overview := tools["get_module_overview"]
	ctx := context.Background()

	text, failed := getContext.Run(ctx, map[string]any{"symbol_id": "mod:payments"})
	if failed {
		t.Fatalf("context failed: %s", text)
	}
	sc := parseModuleContext(t, text)
	first := map[string]any{
		"symbol_id": "mod:payments", "summary": archSummary("payload-a"),
		"expected_self_hash": sc.SelfHash, "expected_dep_hash": sc.DepHash,
		"model_id": "model-v1",
	}
	if text, failed = upsert.Run(ctx, map[string]any{"items": []any{first}}); failed {
		t.Fatalf("first upsert failed: %s", text)
	}

	second := map[string]any{
		"symbol_id": "mod:payments", "summary": archSummary("payload-b"),
		"expected_self_hash": sc.SelfHash, "expected_dep_hash": sc.DepHash,
		"model_id": "model-v2",
	}
	text, failed = upsert.Run(ctx, map[string]any{"items": []any{second}})
	if failed {
		t.Fatalf("second upsert failed: %s", text)
	}
	var result summaryUpsertResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].Status != "updated" || result.Results[0].Error != "" {
		t.Fatalf("second upsert = %+v", result.Results)
	}

	cached, err := store.Lookup(ctx, "mod:payments")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal(cached.Summary, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Intent != "payload-b" || cached.ModelID != "model-v2" || cached.State != cache.StateSynced {
		t.Fatalf("lookup = intent %q model %q state %s", payload.Intent, cached.ModelID, cached.State)
	}

	text, failed = overview.Run(ctx, map[string]any{"module": "mod:payments", "format": "json", "detail": "full"})
	if failed {
		t.Fatalf("overview failed: %s", text)
	}
	var view struct {
		Summary   json.RawMessage `json:"summary"`
		SyncState string          `json:"sync_state"`
	}
	if err := json.Unmarshal([]byte(text), &view); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(view.Summary, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Intent != "payload-b" || view.SyncState != "synced" {
		t.Fatalf("overview summary = %s state=%s", view.Summary, view.SyncState)
	}
}

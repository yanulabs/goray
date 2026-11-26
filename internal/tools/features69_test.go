package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func buildDocumentedFixture(t *testing.T) *index.Index {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/docs\n\ngo 1.26\n",
		"a/a.go": `package a

// Normal returns the normal value.
// It is documented.
func Normal() int { return 1 }

// Also returns two.
func Also() int { return 2 }

// API is a documented interface.
type API interface{ Value() int }

// Thing is a documented struct.
type Thing struct{ A int }
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
	return idx
}

func TestReferencesPaginationAndFilters(t *testing.T) {
	idx := buildProjectionFixture(t)
	target := ""
	for _, sym := range idx.Symbols {
		if sym.Name == "ThirdNormal" {
			target = sym.ID
		}
	}
	if target == "" {
		t.Fatal("ThirdNormal not found")
	}
	idx.XRefs[target] = []index.XRef{
		{TargetID: target, FromPkg: "example.com/proj/modules/alpha", FromMod: "alpha", File: "modules/alpha/a.go", Line: 1},
		{TargetID: target, FromPkg: "example.com/proj/modules/beta", FromMod: "beta", File: "modules/beta/b.go", Line: 2, Test: true},
		{TargetID: target, FromPkg: "example.com/proj/modules/beta", FromMod: "beta", File: "modules/beta/c_test.go", Line: 3, Test: true},
		{TargetID: target, FromPkg: "example.com/proj/modules/beta", FromMod: "beta", File: "modules/beta/d.go", Line: 4},
	}
	tool := projectionTool(t, idx, "get_references", nil)

	type referencesResponse struct {
		Total      int          `json:"total"`
		Offset     int          `json:"offset"`
		Returned   int          `json:"returned"`
		Truncated  bool         `json:"truncated"`
		NextOffset int          `json:"next_offset"`
		Refs       []index.XRef `json:"refs"`
	}
	run := func(args map[string]any) referencesResponse {
		t.Helper()
		if args == nil {
			args = map[string]any{}
		}
		args["symbol_id"] = target
		text, failed := tool.Run(context.Background(), args)
		if failed {
			t.Fatal(text)
		}
		var out referencesResponse
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	all := run(nil)
	if all.Total != 4 || all.Returned != 4 || all.Truncated {
		t.Fatalf("default = %+v", all)
	}
	page := run(map[string]any{"offset": float64(1), "limit": float64(1)})
	if page.Total != 4 || page.Offset != 1 || page.Returned != 1 || !page.Truncated || page.NextOffset != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Refs[0].Line != 2 {
		t.Fatalf("page refs = %+v", page.Refs)
	}
	noTests := run(map[string]any{"include_tests": false})
	if noTests.Total != 2 || noTests.Returned != 2 || noTests.Refs[0].Test || noTests.Refs[1].Test {
		t.Fatalf("no tests = %+v", noTests)
	}
	beta := run(map[string]any{"module": "mod:beta"})
	if beta.Total != 3 || beta.Returned != 3 {
		t.Fatalf("module filter = %+v", beta)
	}
	betaNoTests := run(map[string]any{"module": "beta", "include_tests": false})
	if betaNoTests.Total != 1 || betaNoTests.Refs[0].Line != 4 {
		t.Fatalf("module+test filter = %+v", betaNoTests)
	}
	pkg := run(map[string]any{"package": "modules/beta"})
	if pkg.Total != 3 {
		t.Fatalf("package filter = %+v", pkg)
	}
	empty := run(map[string]any{"offset": float64(99)})
	if empty.Total != 4 || empty.Offset != 4 || empty.Returned != 0 || empty.Truncated || len(empty.Refs) != 0 {
		t.Fatalf("offset past end = %+v", empty)
	}
	text, failed := tool.Run(context.Background(), map[string]any{"symbol_id": target, "module": "missing"})
	if !failed || !strings.Contains(text, "module \"missing\" not found") {
		t.Fatalf("missing module = %q failed=%v", text, failed)
	}
	text, failed = tool.Run(context.Background(), map[string]any{"symbol_id": target, "package": "missing"})
	if !failed || !strings.Contains(text, "package not found") {
		t.Fatalf("missing package = %q failed=%v", text, failed)
	}
}

func TestFunctionBodySingleBackwardCompatibleAndDoc(t *testing.T) {
	idx := buildDocumentedFixture(t)
	tool := projectionTool(t, idx, "get_function_body", nil)
	normal := canonicalName(t, idx, "Normal")

	plain, failed := tool.Run(context.Background(), map[string]any{"symbol_id": normal})
	if failed {
		t.Fatal(plain)
	}
	if !strings.Contains(plain, "func Normal() int { return 1 }") || strings.Contains(plain, "Normal returns the normal value") {
		t.Fatalf("default body = %q", plain)
	}
	if strings.HasPrefix(plain, "{") {
		t.Fatalf("single-symbol output should remain text: %q", plain)
	}

	withDoc, failed := tool.Run(context.Background(), map[string]any{"symbol_id": normal, "include_doc": true})
	if failed {
		t.Fatal(withDoc)
	}
	if !strings.Contains(withDoc, "// Normal returns the normal value.") || !strings.Contains(withDoc, "// It is documented.") {
		t.Fatalf("doc body = %q", withDoc)
	}
	if !strings.Contains(withDoc, "   3 | // Normal returns") {
		t.Fatalf("doc line numbers = %q", withDoc)
	}

	api := canonicalName(t, idx, "API")
	typeBody, failed := tool.Run(context.Background(), map[string]any{"symbol_id": api, "include_doc": true})
	if failed {
		t.Fatal(typeBody)
	}
	if !strings.Contains(typeBody, "type API interface") || !strings.Contains(typeBody, "API is a documented interface") {
		t.Fatalf("type body = %q", typeBody)
	}
}

func TestFunctionBodyBatchAndBudget(t *testing.T) {
	idx := buildDocumentedFixture(t)
	tool := projectionTool(t, idx, "get_function_body", nil)
	normal := canonicalName(t, idx, "Normal")
	also := canonicalName(t, idx, "Also")

	text, failed := tool.Run(context.Background(), map[string]any{
		"symbol_ids":  []any{normal, also},
		"include_doc": true,
	})
	if failed {
		t.Fatal(text)
	}
	var out struct {
		Bodies       []functionBodyEntry `json:"bodies"`
		TruncatedIDs []string            `json:"truncated_ids"`
		MaxChars     int                 `json:"max_chars"`
		IndexState   index.State         `json:"index_state"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Bodies) != 2 || len(out.TruncatedIDs) != 0 || out.MaxChars != 40000 {
		t.Fatalf("batch = %+v", out)
	}
	if !strings.Contains(out.Bodies[0].Source, "Normal returns the normal value") || !strings.Contains(out.Bodies[1].Source, "Also returns two") {
		t.Fatalf("batch sources = %+v", out.Bodies)
	}
	if out.Bodies[0].CanonicalID != normal || out.Bodies[0].Cyclomatic != 1 || out.Bodies[0].Source == "" {
		t.Fatalf("batch entry = %+v", out.Bodies[0])
	}

	truncatedText, failed := tool.Run(context.Background(), map[string]any{
		"symbol_ids": []any{normal, also},
		"max_chars":  float64(1),
	})
	if failed {
		t.Fatal(truncatedText)
	}
	var truncated struct {
		Bodies       []functionBodyEntry `json:"bodies"`
		TruncatedIDs []string            `json:"truncated_ids"`
	}
	if err := json.Unmarshal([]byte(truncatedText), &truncated); err != nil {
		t.Fatal(err)
	}
	if len(truncated.Bodies) != 1 || len(truncated.TruncatedIDs) != 1 || truncated.TruncatedIDs[0] != also {
		t.Fatalf("truncated = %+v", truncated)
	}

	tooMany := make([]any, 11)
	for i := range tooMany {
		tooMany[i] = normal
	}
	text, failed = tool.Run(context.Background(), map[string]any{"symbol_ids": tooMany})
	if !failed || !strings.Contains(text, "maximum of 10") {
		t.Fatalf("11 ids = %q failed=%v", text, failed)
	}

	text, failed = tool.Run(context.Background(), nil)
	if !failed || !strings.Contains(text, "provide 'symbol_id' or 'symbol_ids'") {
		t.Fatalf("empty args = %q failed=%v", text, failed)
	}
	text, failed = tool.Run(context.Background(), map[string]any{"symbol_ids": []any{"missing"}})
	if !failed || !strings.Contains(text, `"error": "symbol_not_found"`) {
		t.Fatalf("missing symbol = %q failed=%v", text, failed)
	}
}

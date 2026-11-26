package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func TestFunctionBodyBatchReturnsPartialResults(t *testing.T) {
	idx := buildDocumentedFixture(t)
	tool := projectionTool(t, idx, "get_function_body", nil)
	normal := canonicalName(t, idx, "Normal")
	also := canonicalName(t, idx, "Also")

	text, failed := tool.Run(context.Background(), map[string]any{
		"symbol_ids": []any{normal, "NormalTypo", also},
	})
	if failed {
		t.Fatal(text)
	}
	var out struct {
		Bodies []functionBodyEntry   `json:"bodies"`
		Errors []functionBodyFailure `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Bodies) != 2 || len(out.Errors) != 1 {
		t.Fatalf("partial batch = %+v", out)
	}
	if out.Bodies[0].Name != "Normal" || out.Bodies[1].Name != "Also" {
		t.Fatalf("resolved bodies = %+v", out.Bodies)
	}
	failure := out.Errors[0]
	if failure.Error != "symbol_not_found" || failure.SymbolID != "NormalTypo" || failure.Input != "NormalTypo" {
		t.Fatalf("failure identity = %+v", failure)
	}
	if len(failure.NearestMatches) == 0 || len(failure.ExpectedFormats) == 0 || failure.Hint == "" {
		t.Fatalf("failure lost resolver context = %+v", failure)
	}

	text, failed = tool.Run(context.Background(), map[string]any{
		"symbol_ids": []any{"MissingOne", "MissingTwo"},
	})
	if !failed {
		t.Fatalf("all-missing batch should fail: %s", text)
	}
	out.Bodies, out.Errors = nil, nil
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Bodies) != 0 || len(out.Errors) != 2 {
		t.Fatalf("all-missing batch = %+v", out)
	}
}

func TestFunctionBodyFailureCarriesResolverContext(t *testing.T) {
	notFound := functionBodyFailureFor("Missing", &index.SymbolNotFoundError{
		Query: "Missing", Suggestions: []string{"sym:a.Missing"},
	})
	if notFound.Error != "symbol_not_found" || notFound.Input != "Missing" ||
		len(notFound.NearestMatches) != 1 || len(notFound.ExpectedFormats) == 0 {
		t.Fatalf("not-found failure = %+v", notFound)
	}

	ambiguous := functionBodyFailureFor("Value", &index.AmbiguousSymbolError{
		Query: "Value", IDs: []string{"sym:a.Value", "sym:b.Value"},
	})
	if ambiguous.Error != "ambiguous_symbol" || len(ambiguous.Candidates) != 2 || ambiguous.Hint == "" {
		t.Fatalf("ambiguous failure = %+v", ambiguous)
	}

	target := functionBodyFailureFor("example.com/m", &index.PackageTargetError{
		Query: "example.com/m", Package: "example.com/m",
	})
	if target.Error != "package_target" || target.Package != "example.com/m" || target.Hint == "" {
		t.Fatalf("package-target failure = %+v", target)
	}

	source := functionBodyFailureFor("m.Broken", errors.New("source range is stale"))
	if source.Error != "source_error" || source.Message != "source range is stale" {
		t.Fatalf("source failure = %+v", source)
	}
}

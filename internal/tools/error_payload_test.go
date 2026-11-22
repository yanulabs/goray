package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/yanulabs/goray/internal/index"
)

func decodePayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return payload
}

func TestSymbolErrorPayloadHandlesWrappedErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantKey string
	}{
		{
			name:    "ambiguous",
			err:     fmt.Errorf("resolve: %w", &index.AmbiguousSymbolError{Query: "Value", IDs: []string{"sym:a.Value", "sym:b.Value"}}),
			wantKey: "ambiguous_symbol",
		},
		{
			name:    "not found",
			err:     fmt.Errorf("resolve: %w", &index.SymbolNotFoundError{Query: "Missing", Suggestions: []string{"sym:a.Missing"}}),
			wantKey: "symbol_not_found",
		},
		{
			name:    "package target",
			err:     fmt.Errorf("resolve: %w", &index.PackageTargetError{Query: "example.com/m", Package: "example.com/m"}),
			wantKey: "package_target",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := decodePayload(t, symbolErrorPayload(test.err))
			if payload["error"] != test.wantKey {
				t.Fatalf("error = %v, want %s (%v)", payload["error"], test.wantKey, payload)
			}
		})
	}
}

func TestSummaryErrorPayloadHandlesWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("summary: %w", &index.SymbolNotFoundError{Query: "Missing"})
	payload := decodePayload(t, summaryErrorPayload("Missing", wrapped))
	if payload["error"] != "symbol_not_found" {
		t.Fatalf("error = %v (%v)", payload["error"], payload)
	}
	if payload["input"] != "Missing" {
		t.Fatalf("input = %v", payload["input"])
	}
}

func TestSymbolErrorPayloadFallsBackForUnknownErrors(t *testing.T) {
	if got := symbolErrorPayload(errors.New("boom")); got != "boom" {
		t.Fatalf("payload = %q", got)
	}
}

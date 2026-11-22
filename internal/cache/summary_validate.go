package cache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yanulabs/goray/internal/index"
)

type funcSummary struct {
	Intent      *string   `json:"intent"`
	SideEffects *[]string `json:"side_effects"`
	Invariants  *[]string `json:"invariants"`
	Concurrency *string   `json:"concurrency"`
}

type archSummary struct {
	Intent        *string   `json:"intent"`
	KeyInvariants *[]string `json:"key_invariants"`
	Boundaries    *[]string `json:"boundaries"`
}

var sideEffectEnum = map[string]bool{
	"none": true, "db_read": true, "db_write": true, "network_io": true,
	"fs_io": true, "exec": true, "mutates_receiver": true, "mutates_global": true,
	"spawns_goroutine": true, "panics": true, "env_read": true,
}

func validateSummary(kind string, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("summary is required")
	}
	if kind == "func" || kind == "method" {
		var payload funcSummary
		if err := strictDecode(raw, &payload); err != nil {
			return err
		}
		if err := validateIntent(payload.Intent); err != nil {
			return err
		}
		if payload.SideEffects == nil || len(*payload.SideEffects) == 0 {
			return errors.New("side_effects must be a non-empty array")
		}
		none := false
		for i, effect := range *payload.SideEffects {
			if !sideEffectEnum[effect] {
				return fmt.Errorf("side_effects[%d]: invalid value %q", i, effect)
			}
			if effect == "none" {
				none = true
			}
		}
		if none && len(*payload.SideEffects) != 1 {
			return errors.New("side_effects: 'none' must be the sole element")
		}
		if payload.Invariants == nil {
			return errors.New("invariants must be an array (may be empty)")
		}
		if err := validateStrings(*payload.Invariants, "invariants"); err != nil {
			return err
		}
		if payload.Concurrency != nil && utf8.RuneCountInString(*payload.Concurrency) > 800 {
			return errors.New("concurrency exceeds 800 characters")
		}
		return nil
	}

	var payload archSummary
	if err := strictDecode(raw, &payload); err != nil {
		return err
	}
	if err := validateIntent(payload.Intent); err != nil {
		return err
	}
	if payload.KeyInvariants == nil {
		return errors.New("key_invariants must be an array (may be empty)")
	}
	if payload.Boundaries == nil {
		return errors.New("boundaries must be an array (may be empty)")
	}
	if err := validateStrings(*payload.KeyInvariants, "key_invariants"); err != nil {
		return err
	}
	return validateStrings(*payload.Boundaries, "boundaries")
}

func strictDecode(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid summary JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid summary JSON: trailing data")
	}
	return nil
}

func validateIntent(intent *string) error {
	if intent == nil || strings.TrimSpace(*intent) == "" {
		return errors.New("intent is required and must not be empty")
	}
	if utf8.RuneCountInString(*intent) > 400 {
		return errors.New("intent exceeds 400 characters")
	}
	return nil
}

func validateStrings(values []string, field string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s[%d] must not be empty", field, i)
		}
		if utf8.RuneCountInString(value) > 400 {
			return fmt.Errorf("%s[%d] exceeds 400 characters", field, i)
		}
	}
	return nil
}

func compactJSON(raw json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readSource(idx *index.Index, sym *WorkingSymbol) string {
	if idx == nil || sym == nil || sym.FilePath == "" || sym.StartLine <= 0 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.FilePath)))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	start := max(sym.StartLine-1, 0)
	end := sym.EndLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// Cached is the report-facing view of one symbol's summary state.

package tools

import (
	"errors"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

// symbolArg reads the canonical symbol_id parameter and its legacy aliases.
func symbolArg(args map[string]any) string {
	return firstStringArg(args, "symbol_id", "symbol", "query", "id")
}

// packageArg reads the canonical package parameter and its legacy aliases.
func packageArg(args map[string]any) string {
	return firstStringArg(args, "package", "pkg_path", "pkg", "root")
}

// moduleArg reads the canonical module parameter and its legacy aliases.
func moduleArg(args map[string]any) string {
	return firstStringArg(args, "module", "module_name", "mod")
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// nonNegativeIntArg honors an explicit zero, which several pagination and
// top-N parameters use to mean "no cap".
func nonNegativeIntArg(args map[string]any, key string, fallback int) int {
	if value, ok := args[key].(float64); ok && value >= 0 {
		return int(value)
	}
	return fallback
}

// pageSizeArg returns the default when the parameter is absent, zero when the
// caller explicitly requests no limit, and the positive value otherwise.
func pageSizeArg(args map[string]any, key string, fallback int) int {
	if value, ok := args[key].(float64); ok {
		if value <= 0 {
			return 0
		}
		return int(value)
	}
	return fallback
}

// symbolErrorPayload turns resolver failures into machine-readable errors with
// candidate IDs so agents can retry without guessing.
func symbolErrorPayload(err error) string {
	var ambiguous *index.AmbiguousSymbolError
	if errors.As(err, &ambiguous) {
		return render.JSON(map[string]any{
			"error":      "ambiguous_symbol",
			"input":      ambiguous.Query,
			"candidates": ambiguous.IDs,
			"hint":       "retry with one of the canonical candidate IDs",
		})
	}
	var notFound *index.SymbolNotFoundError
	if errors.As(err, &notFound) {
		payload := map[string]any{
			"error":            "symbol_not_found",
			"input":            notFound.Query,
			"expected_formats": index.SymbolIDFormats(),
			"hint":             "call lookup_symbol with the same input to discover valid IDs",
		}
		if len(notFound.Suggestions) > 0 {
			payload["nearest_matches"] = notFound.Suggestions
		}
		return render.JSON(payload)
	}
	var packageTarget *index.PackageTargetError
	if errors.As(err, &packageTarget) {
		return render.JSON(map[string]any{
			"error":   "package_target",
			"input":   packageTarget.Query,
			"package": packageTarget.Package,
			"hint":    "use get_api_surface, get_dependency_graph, or get_impact_analysis for package-scoped queries",
		})
	}
	return err.Error()
}

func displaySymbolID(sym *index.Symbol) string {
	if canonical := sym.CanonicalID(); canonical != "" {
		return canonical
	}
	return sym.ID
}

func stringSliceAliasArg(args map[string]any, keys ...string) ([]string, error) {
	for _, key := range keys {
		if _, ok := args[key]; ok {
			return stringSliceArg(args, key)
		}
	}
	return nil, nil
}

package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/yanulabs/goray/internal/diff"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) architectureDiff() mcp.Tool {
	return mcp.Tool{
		Name:        "get_architecture_diff",
		Annotations: readOnlyAnnotations(),
		Description: "Deterministic architecture diff between the live working-tree snapshot and a git base ref: new module cycles, breaking exported API, newly dead code, and untested complexity regressions. Only the base ref is indexed; the working tree reuses the in-memory index. JSON matches 'goray diff --format json'. Triggered fail_on gates are returned in failed_gates on a successful query, not as a tool error.",
		InputSchema: object(map[string]any{
			"base_ref": propString("Git revision to compare against (default: HEAD)"),
			"fail_on":  propStringOrArray("Optional gate(s): cycles, dead_code, breaking_api, untested_hotspots"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			gates, err := failOnArg(args)
			if err != nil {
				return err.Error(), true
			}
			head, err := t.src.Wait(ctx)
			if err != nil {
				if text, ok := indexingPayload(err); ok {
					return text, false
				}
				return err.Error(), true
			}
			report, err := diff.BuildWithHeadContext(ctx, diff.Options{
				BaseRef:   strArg(args, "base_ref", diff.DefaultBaseRef),
				Path:      t.src.Root(),
				Tags:      head.BuildTags,
				Workspace: head.Workspace,
				FailOn:    gates,
			}, head)
			if err != nil {
				return render.JSON(map[string]any{
					"error":   "architecture_diff_failed",
					"message": err.Error(),
				}), true
			}
			return render.JSON(report), false
		},
	}
}

// failOnArg accepts the CLI's comma-separated string form and the MCP-friendly
// array form, validates every gate, and collapses duplicates.
func failOnArg(args map[string]any) ([]string, error) {
	switch value := args["fail_on"].(type) {
	case nil:
		return nil, nil
	case string:
		return diff.NormalizeGates([]string{value})
	case []string:
		return diff.NormalizeGates(value)
	case []any:
		values := make([]string, 0, len(value))
		for i, item := range value {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("fail_on[%d] must be a non-empty string", i)
			}
			values = append(values, text)
		}
		return diff.NormalizeGates(values)
	default:
		return nil, errors.New("fail_on must be a string or an array of strings")
	}
}

package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

var commandTimeout = 3 * time.Minute

const maxCommandOutput = 64 * 1024

func (t *toolset) runTests() mcp.Tool {
	return mcp.Tool{
		Name:        "run_tests",
		Description: "Run 'go test -race -count=1' on one or more package patterns with bounded output. Forward the server's configured build tags.",
		Exclusive:   true,
		InputSchema: object(map[string]any{
			"packages": propStringOrArray("Package pattern(s): a string, whitespace-separated patterns, or an array (default: ./...)"),
			"run":      propString("Optional -run regex"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			cmdArgs, err := testCommandArgs(args, t.src.Status().BuildTags)
			if err != nil {
				return err.Error(), true
			}
			return runCommand(ctx, t.src.Root(), "go", cmdArgs...)
		},
	}
}

func (t *toolset) runLint() mcp.Tool {
	return mcp.Tool{
		Name:        "run_lint",
		Description: "Run 'golangci-lint run' (fallback: 'go vet') on one or more package patterns. Forward the server's configured build tags.",
		Exclusive:   true,
		InputSchema: object(map[string]any{
			"packages": propStringOrArray("Package pattern(s): a string, whitespace-separated patterns, or an array (default: ./...)"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			packages, err := commandPackages(args)
			if err != nil {
				return err.Error(), true
			}
			tags := t.src.Status().BuildTags
			if _, err := exec.LookPath("golangci-lint"); err == nil {
				cmdArgs := []string{"run"}
				if len(tags) > 0 {
					cmdArgs = append(cmdArgs, "--build-tags="+strings.Join(tags, ","))
				}
				cmdArgs = append(cmdArgs, packages...)
				return runCommand(ctx, t.src.Root(), "golangci-lint", cmdArgs...)
			}
			cmdArgs := []string{"vet"}
			if len(tags) > 0 {
				cmdArgs = append(cmdArgs, "-tags="+strings.Join(tags, ","))
			}
			cmdArgs = append(cmdArgs, packages...)
			return runCommand(ctx, t.src.Root(), "go", cmdArgs...)
		},
	}
}

func testCommandArgs(args map[string]any, tags []string) ([]string, error) {
	packages, err := commandPackages(args)
	if err != nil {
		return nil, err
	}
	cmdArgs := []string{"test", "-race", "-count=1"}
	if run := strArg(args, "run", ""); run != "" {
		cmdArgs = append(cmdArgs, "-run", run)
	}
	if len(tags) > 0 {
		cmdArgs = append(cmdArgs, "-tags="+strings.Join(tags, ","))
	}
	return append(cmdArgs, packages...), nil
}

// commandPackages accepts either an array of package patterns or a
// whitespace-separated string. Every token is validated before it reaches the
// go tool: a value such as "-exec=evil" would otherwise be parsed as a flag.
func commandPackages(args map[string]any) ([]string, error) {
	var tokens []string
	switch value := args["packages"].(type) {
	case nil:
	case string:
		tokens = strings.Fields(value)
	case []string:
		for _, item := range value {
			tokens = append(tokens, strings.Fields(item)...)
		}
	case []any:
		for i, item := range value {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("packages[%d] must be a non-empty string", i)
			}
			tokens = append(tokens, strings.Fields(text)...)
		}
	default:
		return nil, errors.New("packages must be a string or an array of strings")
	}
	if len(tokens) == 0 {
		tokens = []string{"./..."}
	}
	for _, token := range tokens {
		if strings.HasPrefix(token, "-") {
			return nil, fmt.Errorf("invalid package %q: package patterns must not start with '-'", token)
		}
	}
	return tokens, nil
}

func (t *toolset) reindex() mcp.Tool {
	return mcp.Tool{
		Name:        "reindex",
		Description: "Rebuild the go/packages + go/types semantic index from disk.",
		Exclusive:   true,
		InputSchema: object(nil),
		Run: func(ctx context.Context, _ map[string]any) (string, bool) {
			if err := t.src.RebuildContext(ctx); err != nil {
				return "reindex failed: " + err.Error(), true
			}
			status := t.src.Status()
			return render.JSON(map[string]any{
				"status":      "ok",
				"index_state": status.IndexState,
				"packages":    status.Packages,
				"symbols":     status.Symbols,
				"build_ms":    status.BuildMS,
			}), false
		},
	}
}

func runCommand(ctx context.Context, dir, bin string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	//nolint:gosec // exec.CommandContext does not invoke a shell; arguments are passed directly.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// Kill descendants that keep stdout open after the parent is canceled.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	out = truncateCommandOutput(out, maxCommandOutput)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return fmt.Sprintf("FAILED (timeout after %s):\n%s", commandTimeout, out), true
		case errors.Is(ctx.Err(), context.Canceled):
			return fmt.Sprintf("FAILED (context canceled):\n%s", out), true
		default:
			return fmt.Sprintf("FAILED (%v):\n%s", err, out), true
		}
	}
	return "OK:\n" + string(out), false
}

func truncateCommandOutput(out []byte, limit int) []byte {
	if len(out) <= limit {
		return out
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	truncated := append([]byte(nil), out[:cut]...)
	return append(truncated, []byte("\n... [output truncated at 64 KiB]")...)
}

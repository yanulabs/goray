package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
		Description: "Run 'go test -race -count=1' on a package pattern with bounded output.",
		Exclusive:   true,
		InputSchema: object(map[string]any{
			"packages": propString("Package pattern (default: ./...)"),
			"run":      propString("Optional -run regex"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			cmdArgs := []string{"test", "-race", "-count=1"}
			if run := strArg(args, "run", ""); run != "" {
				cmdArgs = append(cmdArgs, "-run", run)
			}
			cmdArgs = append(cmdArgs, strArg(args, "packages", "./..."))
			return runCommand(ctx, t.src.Root(), "go", cmdArgs...)
		},
	}
}

func (t *toolset) runLint() mcp.Tool {
	return mcp.Tool{
		Name:        "run_lint",
		Description: "Run 'golangci-lint run' (fallback: 'go vet') on a package pattern.",
		Exclusive:   true,
		InputSchema: object(map[string]any{
			"packages": propString("Package pattern (default: ./...)"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, bool) {
			pkg := strArg(args, "packages", "./...")
			if _, err := exec.LookPath("golangci-lint"); err == nil {
				return runCommand(ctx, t.src.Root(), "golangci-lint", "run", pkg)
			}
			return runCommand(ctx, t.src.Root(), "go", "vet", pkg)
		},
	}
}

func (t *toolset) reindex() mcp.Tool {
	return mcp.Tool{
		Name:        "reindex",
		Description: "Rebuild the go/packages + go/types semantic index from disk.",
		Exclusive:   true,
		InputSchema: object(nil),
		Run: func(context.Context, map[string]any) (string, bool) {
			if err := t.src.Rebuild(); err != nil {
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

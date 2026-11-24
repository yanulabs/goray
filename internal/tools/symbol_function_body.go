package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
)

func (t *toolset) functionBody() mcp.Tool {
	return mcp.Tool{
		Name:        "get_function_body",
		Description: "Exact source, cyclomatic complexity, and LOC of a function or method. Accepts canonical IDs, full Go identifiers, and repository-relative shorthands.",
		InputSchema: object(map[string]any{
			"symbol_id": propString("Function/method ID, full Go identifier, or repository-relative shorthand"),
		}, "symbol_id"),
		Run: t.indexedCtx(func(ctx context.Context, idx *index.Index, args map[string]any) (string, bool) {
			sym, err := idx.SingleSymbol(symbolArg(args))
			if err != nil {
				return symbolErrorPayload(err), true
			}
			if sym.EndLine < sym.Line {
				return "symbol has no source body: " + sym.ID, true
			}
			raw, err := os.ReadFile(filepath.Join(idx.RootDir, filepath.FromSlash(sym.File)))
			if err != nil {
				return err.Error(), true
			}
			lines := strings.Split(string(raw), "\n")
			start := max(sym.Line-1, 0)
			end := min(sym.EndLine, len(lines))
			if start >= end {
				return "source range is stale; reindex and retry", true
			}

			var b strings.Builder
			if state := t.src.Status().IndexState; state != index.StateReady {
				fmt.Fprintf(&b, "// index_state: %s\n", state)
			}
			if canonical := sym.CanonicalID(); canonical != "" {
				fmt.Fprintf(&b, "// canonical_id: %s\n", canonical)
			}
			if summary, state, depsChanged := t.enrich(ctx, sym); len(summary) > 0 {
				fmt.Fprintf(&b, "// sync_state: %s deps_changed=%v\n// summary: %s\n", state, depsChanged, summary)
			}
			fmt.Fprintf(&b, "// %s (%s:%d-%d | LOC=%d | cyclomatic=%d)\n",
				sym.ID, sym.File, sym.Line, sym.EndLine, sym.LOC, sym.Cyclomatic)
			for i := start; i < end; i++ {
				fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
			}
			return b.String(), false
		}),
	}
}

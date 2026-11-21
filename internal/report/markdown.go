package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
)

// MarkdownSummaries appends cached summary coverage and intents to a Markdown
// architecture map.
func MarkdownSummaries(ctx context.Context, idx *index.Index, store *cache.Cache) (string, error) {
	data, err := Build(ctx, idx, store)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\n## Summary Cache\n\n")
	fmt.Fprintf(&b, "- Coverage: %.1f%% (%d / %d eligible)\n",
		data.Coverage.Percent, data.Coverage.Synced, data.Coverage.Eligible)
	fmt.Fprintf(&b, "- States: synced %d, soft_stale %d, hard_stale %d, missing %d, not_eligible %d\n\n",
		data.Coverage.Synced, data.Coverage.SoftStale, data.Coverage.HardStale,
		data.Coverage.Missing, data.Coverage.NotEligible)

	rows := 0
	for _, symbol := range data.Symbols {
		if symbol.Summary == nil {
			continue
		}
		if rows == 0 {
			b.WriteString("| Symbol | Kind | State | Intent |\n|---|---|---|---|\n")
		}
		intent := symbol.Summary.Intent
		if intent == "" {
			intent = "—"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
			markdownCell(symbol.ID), markdownCell(symbol.Kind),
			markdownCell(symbol.SyncState), markdownCell(intent))
		rows++
	}
	if rows == 0 {
		b.WriteString("No cached summaries.\n")
	}
	return b.String(), nil
}

func markdownCell(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	runes := []rune(value)
	if len(runes) > 160 {
		value = string(runes[:157]) + "..."
	}
	return value
}

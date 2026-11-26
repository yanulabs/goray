package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/diff"
)

var diffValueFlags = map[string]bool{"format": true, "fail-on": true, "tags": true}

type diffOptions struct {
	baseRef   string
	path      string
	format    string
	failOn    []string
	tags      string
	workspace bool
}

func parseDiffArgs(args []string) (diffOptions, error) {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	quietFlagSet(fs)
	opts := diffOptions{baseRef: diff.DefaultBaseRef, path: ".", format: "md"}
	fs.StringVar(&opts.format, "format", "md", "output format: md or json")
	var failOn string
	fs.StringVar(&failOn, "fail-on", "", "comma-separated gates: cycles, dead_code, breaking_api, untested_hotspots")
	fs.StringVar(&opts.tags, "tags", "", "Go build tags (comma- or space-separated)")
	fs.BoolVar(&opts.workspace, "workspace", false, "index all modules in the active go.work")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return diffOptions{}, err
	}
	switch opts.format {
	case "md", "json":
	default:
		return diffOptions{}, usagef("unknown diff format %q (use md or json)", opts.format)
	}
	if len(positionals) > 2 {
		return diffOptions{}, usagef("diff accepts at most a base ref and a path")
	}
	switch len(positionals) {
	case 2:
		if positionals[0] != "" {
			opts.baseRef = positionals[0]
		}
		if positionals[1] != "" {
			opts.path = positionals[1]
		}
	case 1:
		if positionals[0] != "" {
			// A single path-like positional is a path, not a base ref:
			// `goray diff ./subpkg` must compare HEAD with that subtree.
			if isDiffPathArg(positionals[0]) {
				opts.path = positionals[0]
			} else {
				opts.baseRef = positionals[0]
			}
		}
	}
	for _, gate := range strings.Split(failOn, ",") {
		gate = strings.TrimSpace(gate)
		if gate == "" {
			continue
		}
		if !diff.ValidGate(gate) {
			return diffOptions{}, usagef("unknown --fail-on gate %q", gate)
		}
		opts.failOn = append(opts.failOn, gate)
	}
	return opts, nil
}

// isDiffPathArg reports whether a single positional argument denotes a path
// rather than a git revision. Explicit relative and absolute paths always do;
// a bare existing directory is also treated as a path.
func isDiffPathArg(arg string) bool {
	if arg == "." || arg == ".." || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || filepath.IsAbs(arg) {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}

func runDiff(args []string, stdout, stderr io.Writer) error {
	opts, err := parseDiffArgs(args)
	if err != nil {
		return err
	}
	report, err := buildDiff(opts, stderr)
	if err != nil {
		return err
	}
	if opts.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := printDiffMarkdown(stdout, report); err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return fmt.Errorf("diff gates failed: %s", strings.Join(report.Failed, ", "))
	}
	return nil
}

func buildDiff(opts diffOptions, stderr io.Writer) (diff.Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return buildDiffContext(ctx, opts, stderr)
}

// buildDiffContext compares the working tree with a git revision. The caller
// owns ctx so long indexing runs can be canceled.
func buildDiffContext(ctx context.Context, opts diffOptions, stderr io.Writer) (diff.Report, error) {
	report, err := diff.BuildContext(ctx, diff.Options{
		BaseRef:   opts.baseRef,
		Path:      opts.path,
		Tags:      buildTags(opts.tags),
		Workspace: opts.workspace,
		FailOn:    opts.failOn,
	})
	if err != nil {
		var usage *diff.UsageError
		if errors.As(err, &usage) {
			return diff.Report{}, usagef("%s", usage.Message)
		}
		return diff.Report{}, err
	}
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "goray diff: base %s (%d symbols) -> head (%d symbols)\n",
			opts.baseRef, report.Summary.SymbolsBase, report.Summary.Symbols)
	}
	return report, nil
}

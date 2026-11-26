package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/index"
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
	opts := diffOptions{baseRef: "HEAD", path: ".", format: "md"}
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
		switch gate {
		case "cycles", "dead_code", "breaking_api", "untested_hotspots":
			opts.failOn = append(opts.failOn, gate)
		default:
			return diffOptions{}, usagef("unknown --fail-on gate %q", gate)
		}
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

func buildDiff(opts diffOptions, stderr io.Writer) (diffReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return buildDiffContext(ctx, opts, stderr)
}

// buildDiffContext compares the working tree with a git revision. The caller
// owns ctx so long indexing runs can be canceled.
func buildDiffContext(ctx context.Context, opts diffOptions, stderr io.Writer) (diffReport, error) {
	repo, err := gitOutput(ctx, opts.path, "rev-parse", "--show-toplevel")
	if err != nil {
		return diffReport{}, usagef("not inside a git repository: %v", err)
	}
	repo = strings.TrimSpace(repo)
	// ResolveProjectRoot canonicalizes symlinks. git reports the path as the
	// user spelled it, so canonicalize both sides before computing the relative
	// subtree (on macOS /var and /private/var would otherwise differ).
	if resolved, resolveErr := filepath.EvalSymlinks(repo); resolveErr == nil {
		repo = resolved
	}
	baseCommit, err := gitOutput(ctx, repo, "rev-parse", "--verify", opts.baseRef+"^{commit}")
	if err != nil {
		return diffReport{}, usagef("cannot resolve base ref %q: %v", opts.baseRef, err)
	}
	baseCommit = strings.TrimSpace(baseCommit)

	headRoot, err := index.ResolveProjectRoot(opts.path, opts.workspace)
	if err != nil {
		return diffReport{}, err
	}
	rel, err := filepath.Rel(repo, headRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		return diffReport{}, usagef("indexed path %s is outside git repository %s", headRoot, repo)
	}

	tmpDir, err := os.MkdirTemp("", "goray-diff-*")
	if err != nil {
		return diffReport{}, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = gitOutput(cleanupCtx, repo, "worktree", "remove", "--force", tmpDir)
		_ = os.RemoveAll(tmpDir)
	}()
	if _, err := gitOutput(ctx, repo, "worktree", "add", "--detach", tmpDir, baseCommit); err != nil {
		return diffReport{}, fmt.Errorf("create base worktree: %w", err)
	}
	baseRoot := filepath.Join(tmpDir, rel)
	if opts.workspace {
		if workspace, ok := index.FindGoWork(baseRoot); ok {
			baseRoot = workspace
		}
	}

	tags := buildTags(opts.tags)
	// Release artifacts after each build: a diff only needs the derived
	// index, and retaining both type graphs at once doubles peak memory.
	buildOpts := index.BuildOptions{Tags: tags, Workspace: opts.workspace, ReleaseArtifacts: true}
	head, err := index.BuildWithOptionsContext(ctx, headRoot, buildOpts)
	if err != nil {
		return diffReport{}, fmt.Errorf("index working tree: %w", err)
	}
	base, err := index.BuildWithOptionsContext(ctx, baseRoot, buildOpts)
	if err != nil {
		return diffReport{}, fmt.Errorf("index base ref: %w", err)
	}
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "goray diff: base %s (%d symbols) -> head (%d symbols)\n", opts.baseRef, len(base.Symbols), len(head.Symbols))
	}
	report := compareIndexes(base, head)
	report.BaseRef = opts.baseRef
	report.BaseCommit = baseCommit
	report.HeadRef = "working tree"
	report.Head = baseCommit
	if headCommit, err := gitOutput(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}"); err == nil {
		report.Head = strings.TrimSpace(headCommit)
	}
	for _, gate := range opts.failOn {
		if gateTriggered(report, gate) {
			report.Failed = append(report.Failed, gate)
		}
	}
	return report, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

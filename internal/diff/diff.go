package diff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanulabs/goray/internal/index"
)

// DefaultBaseRef is the revision compared when no base is provided.
const DefaultBaseRef = "HEAD"

// Options controls an architecture diff. Path defaults to the current
// directory and BaseRef defaults to HEAD. FailOn accepts canonical gate names
// (see GateNames); callers that accept user input should run NormalizeGates
// first.
type Options struct {
	BaseRef   string
	Path      string
	Tags      []string
	Workspace bool
	FailOn    []string
}

// UsageError marks invalid user input (not a git repository, unknown base ref,
// or a path outside the repository). The CLI maps it to exit code 2.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

func normalizeOptions(opts Options) Options {
	if strings.TrimSpace(opts.BaseRef) == "" {
		opts.BaseRef = DefaultBaseRef
	}
	if strings.TrimSpace(opts.Path) == "" {
		opts.Path = "."
	}
	opts.Tags = index.NormalizeTags(opts.Tags)
	return opts
}

func buildOptions(opts Options) index.BuildOptions {
	// Release artifacts after each build: a diff only needs the derived index,
	// and retaining both type graphs at once doubles peak memory.
	return index.BuildOptions{
		Tags:             opts.Tags,
		Workspace:        opts.Workspace,
		ReleaseArtifacts: true,
	}
}

// BuildContext indexes the working tree and a detached temporary worktree of
// the base ref, then compares them. This is the CLI behavior.
func BuildContext(ctx context.Context, opts Options) (Report, error) {
	opts = normalizeOptions(opts)
	repo, baseCommit, err := resolveRepoRef(ctx, opts)
	if err != nil {
		return Report{}, err
	}
	headRoot, err := index.ResolveProjectRoot(opts.Path, opts.Workspace)
	if err != nil {
		return Report{}, err
	}
	rel, err := repoRelative(repo, headRoot)
	if err != nil {
		return Report{}, err
	}
	baseRoot, cleanup, err := addBaseWorktree(ctx, opts, repo, baseCommit, rel)
	if err != nil {
		return Report{}, err
	}
	defer cleanup()

	buildOpts := buildOptions(opts)
	head, err := index.BuildWithOptionsContext(ctx, headRoot, buildOpts)
	if err != nil {
		return Report{}, fmt.Errorf("index working tree: %w", err)
	}
	base, err := index.BuildWithOptionsContext(ctx, baseRoot, buildOpts)
	if err != nil {
		return Report{}, fmt.Errorf("index base ref: %w", err)
	}
	return finishReport(ctx, opts, repo, baseCommit, base, head), nil
}

// BuildWithHeadContext compares an existing live index snapshot (the working
// tree) against a base ref. Only the base ref is indexed, so MCP callers never
// rebuild the working tree they already have in memory.
func BuildWithHeadContext(ctx context.Context, opts Options, head *index.Index) (Report, error) {
	if head == nil {
		return Report{}, errors.New("head index is required")
	}
	opts = normalizeOptions(opts)
	repo, baseCommit, err := resolveRepoRef(ctx, opts)
	if err != nil {
		return Report{}, err
	}
	headRoot := head.RootDir
	if headRoot == "" {
		headRoot = opts.Path
	}
	if headRoot == "" {
		return Report{}, errors.New("head index has no root directory")
	}
	rel, err := repoRelative(repo, headRoot)
	if err != nil {
		return Report{}, err
	}
	baseRoot, cleanup, err := addBaseWorktree(ctx, opts, repo, baseCommit, rel)
	if err != nil {
		return Report{}, err
	}
	defer cleanup()

	base, err := index.BuildWithOptionsContext(ctx, baseRoot, buildOptions(opts))
	if err != nil {
		return Report{}, fmt.Errorf("index base ref: %w", err)
	}
	return finishReport(ctx, opts, repo, baseCommit, base, head), nil
}

// resolveRepoRef resolves the git repository and base commit before any project
// root resolution, so a non-git directory reports a usage error rather than a
// module-loading failure.
func resolveRepoRef(ctx context.Context, opts Options) (repo, baseCommit string, err error) {
	repo, err = gitOutput(ctx, opts.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", &UsageError{Message: fmt.Sprintf("not inside a git repository: %v", err)}
	}
	repo = strings.TrimSpace(repo)
	if resolved, resolveErr := filepath.EvalSymlinks(repo); resolveErr == nil {
		repo = resolved
	}
	baseCommit, err = gitOutput(ctx, repo, "rev-parse", "--verify", opts.BaseRef+"^{commit}")
	if err != nil {
		return "", "", &UsageError{Message: fmt.Sprintf("cannot resolve base ref %q: %v", opts.BaseRef, err)}
	}
	return repo, strings.TrimSpace(baseCommit), nil
}

// repoRelative computes the indexed subtree relative to the repository root.
// Both sides are symlink-canonicalized so subdirectory and symlinked checkouts
// produce the same relative path.
func repoRelative(repo, headRoot string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(headRoot); err == nil {
		headRoot = resolved
	}
	rel, err := filepath.Rel(repo, headRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", &UsageError{Message: fmt.Sprintf("indexed path %s is outside git repository %s", headRoot, repo)}
	}
	return rel, nil
}

// addBaseWorktree creates a detached worktree for the base commit and returns
// the corresponding indexed root plus a cleanup function. Cleanup runs in both
// success and failure paths.
func addBaseWorktree(ctx context.Context, opts Options, repo, baseCommit, rel string) (baseRoot string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "goray-diff-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = gitOutput(cleanupCtx, repo, "worktree", "remove", "--force", tmpDir)
		_ = os.RemoveAll(tmpDir)
	}
	if _, err := gitOutput(ctx, repo, "worktree", "add", "--detach", tmpDir, baseCommit); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create base worktree: %w", err)
	}
	baseRoot = filepath.Join(tmpDir, rel)
	if opts.Workspace {
		if workspace, ok := index.FindGoWork(baseRoot); ok {
			baseRoot = workspace
		}
	}
	return baseRoot, cleanup, nil
}

func finishReport(ctx context.Context, opts Options, repo, baseCommit string, base, head *index.Index) Report {
	report := Compare(base, head)
	report.BaseRef = opts.BaseRef
	report.BaseCommit = baseCommit
	report.HeadRef = "working tree"
	report.Head = baseCommit
	if headCommit, err := gitOutput(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}"); err == nil {
		report.Head = strings.TrimSpace(headCommit)
	}
	report.Failed = FailedGates(report, opts.FailOn)
	return report
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

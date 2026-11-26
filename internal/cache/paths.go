package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/yanulabs/goray/internal/index"
)

// ReadModulePath reads the module directive from go.mod without loading
// packages. It is side-effect free and safe to call while resolving paths.
func ReadModulePath(canonicalRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(canonicalRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	if path := modfile.ModulePath(data); path != "" {
		return path, nil
	}
	if path := parseModuleDirective(data); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("go.mod in %s has no module directive", canonicalRoot)
}

// parseModuleDirective is a conservative fallback for go.mod files that
// modfile.ModulePath rejects. It handles quoted paths, raw strings, and //
// comments.
func parseModuleDirective(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "module" && !strings.HasPrefix(line, "module ") && !strings.HasPrefix(line, "module\t") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		if rest == "" {
			continue
		}
		if rest[0] == '"' || rest[0] == '`' {
			if path, err := strconv.Unquote(rest); err == nil && path != "" {
				return path
			}
			continue
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// PathOptions selects the workspace context and build tags that determine a
// cache identity. Workspace must be set explicitly: a root can contain both
// go.mod and go.work, and single-module and workspace indexes must not share a
// working database.
type PathOptions struct {
	Tags      []string
	Workspace bool
}

// DefaultPath returns the per-checkout working database path outside the
// repository. Distinct worktrees of the same module resolve to different
// files because the canonical checkout root is hashed.
func DefaultPath(projectRoot string) (string, error) {
	return DefaultPathWithOptions(projectRoot, PathOptions{})
}

// DefaultPathWithTags returns the per-checkout working database path for the
// given build tag set. Non-empty tag sets get a distinct database so an
// integration-tag index cannot overwrite the default snapshot.
func DefaultPathWithTags(projectRoot string, tags []string) (string, error) {
	return DefaultPathWithOptions(projectRoot, PathOptions{Tags: tags})
}

// DefaultPathWithOptions returns the per-checkout working database path for the
// given workspace mode and build tags.
func DefaultPathWithOptions(projectRoot string, opts PathOptions) (string, error) {
	root, err := resolveRoot(projectRoot, opts.Workspace)
	if err != nil {
		return "", err
	}
	return defaultWorktreePath(root, opts.Tags, opts.Workspace)
}

// DefaultSummaryPathWithTags returns the shared summary database path. Build
// tags do not affect it: summary hashes are content-addressed.
func DefaultSummaryPathWithTags(projectRoot string, _ []string) (string, error) {
	return DefaultSummaryPath(projectRoot)
}

// DefaultSummaryPath returns the content-addressed shared summary database
// path. Worktrees and clones of the same module path resolve to the same file.
func DefaultSummaryPath(projectRoot string) (string, error) {
	return DefaultSummaryPathWithOptions(projectRoot, PathOptions{})
}

// DefaultSummaryPathWithOptions returns the shared summary database path for
// the selected workspace mode.
func DefaultSummaryPathWithOptions(projectRoot string, opts PathOptions) (string, error) {
	root, err := resolveRoot(projectRoot, opts.Workspace)
	if err != nil {
		return "", err
	}
	return defaultSummaryPath(root, opts.Workspace)
}

func defaultWorktreePath(canonicalRoot string, tags []string, workspace bool) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	tags = index.NormalizeTags(tags)
	key := canonicalRoot
	if workspace {
		key += "\x00workspace"
	}
	if len(tags) > 0 {
		key += "\x00" + strings.Join(tags, ",")
	}
	name := RepoSlug(canonicalRoot) + "-" + shortHash(key) + ".db"
	return filepath.Join(base, "goray", "worktrees", name), nil
}

func defaultSummaryPath(canonicalRoot string, workspace bool) (string, error) {
	identity, err := projectIdentity(canonicalRoot, workspace)
	if err != nil {
		return "", err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := RepoSlug(identity) + "-" + shortHash(identity) + ".db"
	return filepath.Join(base, "goray", "summaries", name), nil
}

// projectIdentity is the deterministic summary-cache identity: the module path
// for a single module, or the sorted set of workspace module paths for a
// go.work workspace. Workspace mode always parses the active workspace file,
// even when the root also contains go.mod.
func projectIdentity(canonicalRoot string, workspace bool) (string, error) {
	if !workspace {
		if modulePath, err := ReadModulePath(canonicalRoot); err == nil {
			return modulePath, nil
		}
	}
	workFile, ok := index.ActiveGoWorkFile(canonicalRoot)
	if !ok {
		return "", fmt.Errorf("read project identity: no go.mod or go.work found in %s", canonicalRoot)
	}
	data, err := os.ReadFile(workFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", workFile, err)
	}
	work, err := modfile.ParseWork(workFile, data, nil)
	if err != nil {
		return "", fmt.Errorf("parse go.work: %w", err)
	}
	baseDir := filepath.Dir(workFile)
	modules := make([]string, 0, len(work.Use))
	for _, use := range work.Use {
		dir := use.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		if path, err := ReadModulePath(filepath.Clean(dir)); err == nil {
			modules = append(modules, path)
		}
	}
	if len(modules) == 0 {
		return "", fmt.Errorf("cannot determine workspace identity for %s", canonicalRoot)
	}
	sort.Strings(modules)
	return "workspace:" + strings.Join(modules, ","), nil
}

// ResolvePaths applies flag, env, and default precedence. An explicit --db or
// GORAY_DB (including :memory:) puts the store in single-database mode, so both
// returned paths are identical.
func ResolvePaths(projectRoot, flagPath string) (string, string, error) {
	return ResolvePathsWithTags(projectRoot, flagPath, nil)
}

// ResolvePathsWithTags is ResolvePaths with build-tag-aware working DB
// selection. The shared summary DB is tag-independent.
func ResolvePathsWithTags(projectRoot, flagPath string, tags []string) (string, string, error) {
	return ResolvePathsWithOptions(projectRoot, flagPath, PathOptions{Tags: tags})
}

// ResolvePathsWithOptions applies flag, env, workspace, and default precedence.
// An explicit --db or GORAY_DB (including :memory:) puts the store in
// single-database mode, so both returned paths are identical.
func ResolvePathsWithOptions(projectRoot, flagPath string, opts PathOptions) (string, string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, path, err
	}
	root, err := resolveRoot(projectRoot, opts.Workspace)
	if err != nil {
		return "", "", err
	}
	working, err := defaultWorktreePath(root, opts.Tags, opts.Workspace)
	if err != nil {
		return "", "", err
	}
	summary, err := defaultSummaryPath(root, opts.Workspace)
	if err != nil {
		return "", "", err
	}
	return working, summary, nil
}

// resolveRoot accepts either a module root or a go.work workspace root.
// Workspace mode selects the active workspace even when the root also contains
// a go.mod file.
func resolveRoot(projectRoot string, workspace bool) (string, error) {
	if workspace {
		if root, ok := index.FindGoWork(projectRoot); ok {
			return root, nil
		}
		return "", fmt.Errorf("no go.work found for %s; unset workspace mode to index a single module", projectRoot)
	}
	root, err := index.ResolveModuleRoot(projectRoot)
	if err == nil {
		return root, nil
	}
	if workspace, ok := index.FindGoWork(projectRoot); ok {
		return workspace, nil
	}
	return "", err
}

// ResolvePath returns only the working database path without deriving or
// reading the module path.
func ResolvePath(projectRoot, flagPath string) (string, error) {
	return ResolvePathWithTags(projectRoot, flagPath, nil)
}

// ResolvePathWithTags returns only the working database path with build-tag
// aware selection.
func ResolvePathWithTags(projectRoot, flagPath string, tags []string) (string, error) {
	return ResolvePathWithOptions(projectRoot, flagPath, PathOptions{Tags: tags})
}

// ResolvePathWithOptions returns only the working database path with workspace
// and build-tag aware selection.
func ResolvePathWithOptions(projectRoot, flagPath string, opts PathOptions) (string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, err
	}
	return DefaultPathWithOptions(projectRoot, opts)
}

// ResolveSummaryPath returns only the shared summary database path. Explicit
// --db / GORAY_DB overrides resolve to that same path.
func ResolveSummaryPath(projectRoot, flagPath string) (string, error) {
	return ResolveSummaryPathWithOptions(projectRoot, flagPath, PathOptions{})
}

// ResolveSummaryPathWithOptions returns only the shared summary database path
// with workspace-aware identity selection.
func ResolveSummaryPathWithOptions(projectRoot, flagPath string, opts PathOptions) (string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, err
	}
	return DefaultSummaryPathWithOptions(projectRoot, opts)
}

func overridePath(flagPath string) (string, bool, error) {
	path := flagPath
	if path == "" {
		path = os.Getenv("GORAY_DB")
	}
	if path == "" {
		return "", false, nil
	}
	if path == ":memory:" {
		return path, true, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", true, err
	}
	return abs, true, nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

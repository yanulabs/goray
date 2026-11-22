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

// DefaultPath returns the per-checkout working database path outside the
// repository. Distinct worktrees of the same module resolve to different
// files because the canonical checkout root is hashed.
func DefaultPath(projectRoot string) (string, error) {
	root, err := resolveRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return defaultWorktreePath(root, nil)
}

// DefaultPathWithTags returns the per-checkout working database path for the
// given build tag set. Non-empty tag sets get a distinct database so an
// integration-tag index cannot overwrite the default snapshot.
func DefaultPathWithTags(projectRoot string, tags []string) (string, error) {
	root, err := resolveRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return defaultWorktreePath(root, tags)
}

// DefaultSummaryPathWithTags returns the shared summary database path. Build
// tags do not affect it: summary hashes are content-addressed.
func DefaultSummaryPathWithTags(projectRoot string, _ []string) (string, error) {
	return DefaultSummaryPath(projectRoot)
}

// DefaultSummaryPath returns the content-addressed shared summary database
// path. Worktrees and clones of the same module path resolve to the same file.
func DefaultSummaryPath(projectRoot string) (string, error) {
	root, err := resolveRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return defaultSummaryPath(root)
}

func defaultWorktreePath(canonicalRoot string, tags []string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	tags = index.NormalizeTags(tags)
	key := canonicalRoot
	if len(tags) > 0 {
		key += "\x00" + strings.Join(tags, ",")
	}
	name := RepoSlug(canonicalRoot) + "-" + shortHash(key) + ".db"
	return filepath.Join(base, "goray", "worktrees", name), nil
}

func defaultSummaryPath(canonicalRoot string) (string, error) {
	identity, err := projectIdentity(canonicalRoot)
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
// go.work workspace.
func projectIdentity(canonicalRoot string) (string, error) {
	if modulePath, err := ReadModulePath(canonicalRoot); err == nil {
		return modulePath, nil
	}
	data, err := os.ReadFile(filepath.Join(canonicalRoot, "go.work"))
	if err != nil {
		return "", fmt.Errorf("read project identity: %w", err)
	}
	work, err := modfile.ParseWork(filepath.Join(canonicalRoot, "go.work"), data, nil)
	if err != nil {
		return "", fmt.Errorf("parse go.work: %w", err)
	}
	modules := make([]string, 0, len(work.Use))
	for _, use := range work.Use {
		dir := use.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(canonicalRoot, dir)
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
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, path, err
	}
	root, err := resolveRoot(projectRoot)
	if err != nil {
		return "", "", err
	}
	working, err := defaultWorktreePath(root, tags)
	if err != nil {
		return "", "", err
	}
	summary, err := defaultSummaryPath(root)
	if err != nil {
		return "", "", err
	}
	return working, summary, nil
}

// resolveRoot accepts either a module root or a go.work workspace root.
func resolveRoot(projectRoot string) (string, error) {
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
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, err
	}
	return DefaultPathWithTags(projectRoot, tags)
}

// ResolveSummaryPath returns only the shared summary database path. Explicit
// --db / GORAY_DB overrides resolve to that same path.
func ResolveSummaryPath(projectRoot, flagPath string) (string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, err
	}
	return DefaultSummaryPath(projectRoot)
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

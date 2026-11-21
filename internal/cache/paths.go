package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
	root, err := index.ResolveModuleRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return defaultWorktreePath(root)
}

// DefaultSummaryPath returns the content-addressed shared summary database
// path. Worktrees and clones of the same module path resolve to the same file.
func DefaultSummaryPath(projectRoot string) (string, error) {
	root, err := index.ResolveModuleRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return defaultSummaryPath(root)
}

func defaultWorktreePath(canonicalRoot string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := RepoSlug(canonicalRoot) + "-" + shortHash(canonicalRoot) + ".db"
	return filepath.Join(base, "goray", "worktrees", name), nil
}

func defaultSummaryPath(canonicalRoot string) (string, error) {
	modulePath, err := ReadModulePath(canonicalRoot)
	if err != nil {
		return "", err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := RepoSlug(modulePath) + "-" + shortHash(modulePath) + ".db"
	return filepath.Join(base, "goray", "summaries", name), nil
}

// ResolvePaths applies flag, env, and default precedence. An explicit --db or
// GORAY_DB (including :memory:) puts the store in single-database mode, so both
// returned paths are identical.
func ResolvePaths(projectRoot, flagPath string) (string, string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, path, err
	}
	root, err := index.ResolveModuleRoot(projectRoot)
	if err != nil {
		return "", "", err
	}
	working, err := defaultWorktreePath(root)
	if err != nil {
		return "", "", err
	}
	summary, err := defaultSummaryPath(root)
	if err != nil {
		return "", "", err
	}
	return working, summary, nil
}

// ResolvePath returns only the working database path without deriving or
// reading the module path.
func ResolvePath(projectRoot, flagPath string) (string, error) {
	if path, ok, err := overridePath(flagPath); ok || err != nil {
		return path, err
	}
	return DefaultPath(projectRoot)
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

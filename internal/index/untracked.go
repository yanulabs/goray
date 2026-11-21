package index

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const untrackedTimeout = 2 * time.Second

// detectUntracked returns module-relative paths that git reports as untracked.
// It is best-effort: any failure (no git, not a repository, timeout) yields an
// empty set with no error. Untracked files remain in the semantic index.
func detectUntracked(root string) map[string]bool {
	out := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), untrackedTimeout)
	defer cancel()

	topCmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel")
	topOut, err := topCmd.Output()
	if err != nil {
		return out
	}
	top := strings.TrimSpace(string(topOut))
	if top == "" {
		return out
	}
	// macOS temp directories are symlinked; make both sides canonical before
	// computing the module root's prefix within the git work tree.
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	canonicalRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		canonicalRoot = resolved
	}
	relRoot, err := filepath.Rel(top, canonicalRoot)
	if err != nil {
		return out
	}
	prefix := ""
	if relRoot != "." {
		prefix = filepath.ToSlash(relRoot) + "/"
	}

	statusCmd := exec.CommandContext(ctx, "git", "-C", top, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	raw, err := statusCmd.Output()
	if err != nil {
		return out
	}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) < 4 || entry[0] != '?' || entry[1] != '?' || entry[2] != ' ' {
			continue
		}
		gitPath := filepath.ToSlash(string(entry[3:]))
		if prefix != "" {
			if !strings.HasPrefix(gitPath, prefix) {
				continue
			}
			gitPath = strings.TrimPrefix(gitPath, prefix)
		}
		if gitPath != "" {
			out[gitPath] = true
		}
	}
	return out
}

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveModuleRoot finds the nearest directory containing a go.mod file.
// Input may be a module root, any subdirectory, or a file inside the module.
// An empty input defaults to the current directory. The search stops at an
// enclosing go.work that disables module discovery unless --workspace is used
// through ResolveProjectRoot.
func ResolveModuleRoot(inputPath string) (string, error) {
	if inputPath == "" {
		inputPath = "."
	}
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", inputPath, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", inputPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat project path %q: %w", inputPath, err)
	}

	dir := resolved
	if !info.IsDir() {
		dir = filepath.Dir(resolved)
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if modInfo, err := os.Stat(candidate); err == nil && !modInfo.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no go.mod found in %s or any parent directory; run inside a Go module or pass its directory", abs)
}

// workspaceRootAt reports whether dir is a go.work workspace root without a
// go.mod of its own.
func workspaceRootAt(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.work")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return false
	}
	return os.Getenv("GOWORK") != "off"
}

// FindGoWork locates the active go.work for inputPath. GOWORK=off disables
// detection; GOWORK=<path> selects an explicit workspace file.
func FindGoWork(inputPath string) (string, bool) {
	switch env := strings.TrimSpace(os.Getenv("GOWORK")); env {
	case "off":
		return "", false
	case "":
	default:
		info, err := os.Stat(env)
		if err != nil || info.IsDir() {
			return "", false
		}
		return filepath.Dir(env), true
	}
	if inputPath == "" {
		inputPath = "."
	}
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false
	}
	dir := abs
	if !info.IsDir() {
		dir = filepath.Dir(abs)
	}
	for {
		if candidate := filepath.Join(dir, "go.work"); fileExists(candidate) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// ResolveProjectRoot resolves a path to either a single module root or, when
// workspace is true, an enclosing go.work workspace root.
func ResolveProjectRoot(inputPath string, workspace bool) (string, error) {
	if inputPath == "" {
		inputPath = "."
	}
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", inputPath, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", inputPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat project path %q: %w", inputPath, err)
	}
	dir := resolved
	if !info.IsDir() {
		dir = filepath.Dir(resolved)
	}

	if workspace {
		root, ok := FindGoWork(dir)
		if !ok {
			return "", fmt.Errorf("no go.work found in %s or any parent directory; unset --workspace to index a single module", dir)
		}
		return root, nil
	}
	if workspaceRootAt(dir) {
		return dir, nil
	}
	return ResolveModuleRoot(dir)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

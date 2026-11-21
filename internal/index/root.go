package index

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveModuleRoot finds the nearest directory containing a go.mod file.
// Input may be a module root, any subdirectory, or a file inside the module.
// An empty input defaults to the current directory.
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

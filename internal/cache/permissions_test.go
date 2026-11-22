package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenCreatesPrivateCacheDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "private")
	c, err := Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("cache directory mode = %o, want 700", got)
	}
}

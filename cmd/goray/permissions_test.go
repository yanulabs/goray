package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePortFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "run", "port")
	if err := writePortFile(path, "http://127.0.0.1:4321/"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("port file mode = %o, want 600", got)
	}
}

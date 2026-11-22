package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitChange(t *testing.T, changes <-chan []string) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestWatcherRecursiveAndIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	changes := make(chan []string, 1)
	notify := func(paths []string) {
		select {
		case changes <- paths:
		default:
		}
	}
	watcher, err := New(dir, nil, notify)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes)

	// A newly created directory is added dynamically.
	newDir := filepath.Join(dir, "newpkg")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(newDir, "b.go"), []byte("package newpkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes)

	// Ignored directories and editor temp files never notify.
	time.Sleep(100 * time.Millisecond)
drain:
	for {
		select {
		case <-changes:
		default:
			break drain
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "v.go"), []byte("package vendor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go.swp"), []byte("temp"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("ignored path produced a change notification")
	case <-time.After(300 * time.Millisecond):
	}
}

package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func waitChange(t *testing.T, changes <-chan []string) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestNewRejectsMissingRoot(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "missing"), nil, nil); err == nil {
		t.Fatal("expected an error for a missing watch root")
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

func TestRelevantWorkspaceFiles(t *testing.T) {
	cases := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{name: "go.work write", event: fsnotify.Event{Name: "/tmp/ws/go.work", Op: fsnotify.Write}, want: true},
		{name: "go.work create", event: fsnotify.Event{Name: "/tmp/ws/go.work", Op: fsnotify.Create}, want: true},
		{name: "go.work.sum write", event: fsnotify.Event{Name: "/tmp/ws/go.work.sum", Op: fsnotify.Write}, want: true},
		{name: "go source", event: fsnotify.Event{Name: "/tmp/ws/a.go", Op: fsnotify.Write}, want: true},
		{name: "go.mod", event: fsnotify.Event{Name: "/tmp/ws/go.mod", Op: fsnotify.Write}, want: true},
		{name: "read only", event: fsnotify.Event{Name: "/tmp/ws/a.go", Op: fsnotify.Chmod}, want: false},
		{name: "editor temp", event: fsnotify.Event{Name: "/tmp/ws/a.go.swp", Op: fsnotify.Write}, want: false},
		{name: "unrelated file", event: fsnotify.Event{Name: "/tmp/ws/notes.txt", Op: fsnotify.Write}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relevant(tc.event); got != tc.want {
				t.Fatalf("relevant(%+v) = %v, want %v", tc.event, got, tc.want)
			}
		})
	}
}

func TestNewWithRootsWatchesExternalWorkspaceModule(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	sibling := filepath.Join(base, "sibling")
	for _, dir := range []string{workspace, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	changes := make(chan []string, 1)
	notify := func(paths []string) {
		select {
		case changes <- paths:
		default:
		}
	}
	watcher, err := NewWithRoots([]string{workspace, sibling}, nil, notify)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	if err := os.WriteFile(filepath.Join(sibling, "s.go"), []byte("package sibling\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-changes:
		if len(got) != 1 || filepath.Base(got[0]) != "s.go" {
			t.Fatalf("change = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an external module change")
	}
}

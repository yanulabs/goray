package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreWaitReturnsIndexingErrorWhileBuilding(t *testing.T) {
	previous := indexWaitGrace
	indexWaitGrace = 60 * time.Millisecond
	defer func() { indexWaitGrace = previous }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/coldstart\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package coldstart\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir, nil)
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	store.SetBuildHook(func(*Index) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	})
	store.Start()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("initial build never reached the hook")
	}

	_, err := store.Wait(context.Background())
	var building *IndexingError
	if !errors.As(err, &building) {
		t.Fatalf("expected IndexingError, got %v", err)
	}
	if building.ElapsedSeconds() <= 0 || building.State != StateRebuilding {
		t.Fatalf("unexpected indexing error: %+v", building)
	}

	close(release)
	store.WaitForBuild()
	if _, err := store.Wait(context.Background()); err != nil {
		t.Fatalf("wait after build: %v", err)
	}
}

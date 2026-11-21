package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yanulabs/goray/internal/watch"
)

func TestStoreAutoRebuildCoalescing(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/storetest\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package storetest\n\nfunc A() int { return 1 }\n")

	store := NewStore(dir, nil)
	var builds atomic.Int32
	store.SetBuildHook(func(*Index) error {
		builds.Add(1)
		return nil
	})
	store.Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go store.AutoRebuild(ctx)

	if _, err := store.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 || store.State() != StateReady {
		t.Fatalf("initial builds=%d state=%s", builds.Load(), store.State())
	}

	write("package storetest\n\nfunc A() int { return 2 }\n")
	for i := 0; i < 10; i++ {
		store.MarkDirty()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if builds.Load() >= 2 && store.State() == StateReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if builds.Load() != 2 || store.State() != StateReady {
		t.Fatalf("after edit builds=%d state=%s", builds.Load(), store.State())
	}
	time.Sleep(700 * time.Millisecond)
	if builds.Load() != 2 {
		t.Fatalf("rebuilds not coalesced: %d", builds.Load())
	}
}

func TestStoreWaitBoundedWhileDirty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/storetest\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package storetest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir, nil)
	store.Start()
	if _, err := store.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.MarkDirty()
	start := time.Now()
	if _, err := store.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("dirty wait = %s", elapsed)
	}
}

func TestWatcherBurstCoalescesAndKeepsFinalState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/burst\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package burst\n\nfunc Base() int { return 0 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir, nil)
	var builds atomic.Int32
	store.SetBuildHook(func(*Index) error { builds.Add(1); return nil })
	store.Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := store.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.StartAutoRebuild(ctx)
	watcher, err := watch.New(dir, nil, store.MarkDirty)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = watcher.Close()
		store.WaitForAuto()
		store.WaitForBuild()
	}()

	for i := 0; i < 60; i++ {
		name := filepath.Join(dir, fmt.Sprintf("burst%02d.go", i))
		body := fmt.Sprintf("package burst\n\nfunc Fn%02d() int { return %d }\n", i, i)
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	var final *Index
	for time.Now().Before(deadline) {
		idx, err := store.Wait(ctx)
		if err == nil {
			if sym := idx.Symbols["example.com/burst.Fn59"]; sym != nil {
				final = idx
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final == nil {
		t.Fatalf("final file not indexed; builds=%d state=%s", builds.Load(), store.State())
	}
	if sym := final.Symbols["example.com/burst.Fn00"]; sym == nil {
		t.Fatal("first burst file missing")
	}
	if builds.Load() > 4 {
		t.Fatalf("burst was not coalesced: %d builds", builds.Load())
	}

	closed := make(chan struct{})
	go func() {
		cancel()
		_ = watcher.Close()
		store.WaitForAuto()
		store.WaitForBuild()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher/store shutdown leaked")
	}
}

package cache

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modernsqlite "modernc.org/sqlite"

	"github.com/yanulabs/goray/internal/index"
)

// TestMain lets lock tests hold a real cross-process SQLite lock: the child
// process enters this branch and blocks until the parent kills it.
func TestMain(m *testing.M) {
	if path := os.Getenv("GORAY_CACHE_LOCK_DB"); path != "" {
		holdCacheLock(path)
		return
	}
	os.Exit(m.Run())
}

func holdCacheLock(path string) {
	ctx := context.Background()
	connector, err := modernsqlite.NewConnector(dbURL(path, "mode=rw"))
	if err != nil {
		os.Exit(3)
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = conn.Close() }()
	if err := execConn(ctx, conn, "BEGIN IMMEDIATE", nil); err != nil {
		os.Exit(3)
	}
	if err := execConn(ctx, conn, "INSERT INTO cache_meta (key, value) VALUES ('lock_holder', '1')", nil); err != nil {
		os.Exit(3)
	}
	fmt.Println("LOCKED")
	_, _ = io.ReadAll(os.Stdin)
}

func startLockHolder(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCacheLockHelper")
	cmd.Env = append(os.Environ(), "GORAY_CACHE_LOCK_DB="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "LOCKED" {
		_ = cmd.Process.Kill()
		t.Fatalf("lock holder did not report LOCKED: %q (%v)", line, err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func makeDBPairAt(t *testing.T, working, summary, canonicalRoot string) {
	t.Helper()
	c, err := OpenSplit(working, summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background(), &index.Index{RootDir: canonicalRoot, Summary: &index.SummaryData{}}); err != nil {
		_ = c.Close()
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeDBPair(t *testing.T, root string) (string, string) {
	t.Helper()
	working := filepath.Join(root, "worktrees", "checkout.db")
	summary := filepath.Join(root, "summaries", "module.db")
	makeDBPairAt(t, working, summary, root)
	return working, summary
}

func TestCanonicalRootPersisted(t *testing.T) {
	root := t.TempDir()
	working, _ := makeDBPair(t, root)
	got, err := ReadDBMeta(working, canonicalKey)
	if err != nil || got != root {
		t.Fatalf("canonical_root = %q (%v), want %q", got, err, root)
	}
}

func TestRemoveDBTargetsAndDryRun(t *testing.T) {
	root := t.TempDir()
	working, summary := makeDBPair(t, root)
	sidecars := []string{working + "-wal", working + "-shm"}
	for _, path := range sidecars {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dry, err := RemoveDB(working, RemoveOptions{DryRun: true})
	if err != nil || len(dry.Files) != 3 || dry.Removed {
		t.Fatalf("dry run = %+v (%v)", dry, err)
	}
	if _, err := os.Stat(working); err != nil {
		t.Fatal("dry run removed the database")
	}

	result, err := RemoveDB(working, RemoveOptions{})
	if err != nil || !result.Removed || len(result.Files) != 3 {
		t.Fatalf("remove = %+v (%v)", result, err)
	}
	for _, path := range append([]string{working}, sidecars...) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", path, err)
		}
	}
	if _, err := os.Stat(summary); err != nil {
		t.Fatalf("shared summary DB was removed: %v", err)
	}

	again, err := RemoveDB(working, RemoveOptions{})
	if err != nil || again.Removed || len(again.Files) != 0 {
		t.Fatalf("idempotent remove = %+v (%v)", again, err)
	}
	if _, err := RemoveDB(":memory:", RemoveOptions{}); err == nil {
		t.Fatal(":memory: should not be removable")
	}
}

func TestRemoveDBLockProtection(t *testing.T) {
	root := t.TempDir()
	working, _ := makeDBPair(t, root)
	startLockHolder(t, working)

	if _, err := RemoveDB(working, RemoveOptions{}); err == nil || !strings.Contains(err.Error(), "in use by another process") {
		t.Fatalf("locked remove should fail with a clear error, got %v", err)
	}
	if _, err := os.Stat(working); err != nil {
		t.Fatalf("locked DB was removed: %v", err)
	}

	result, err := RemoveDB(working, RemoveOptions{Force: true})
	if err != nil || !result.Removed {
		t.Fatalf("forced remove = %+v (%v)", result, err)
	}
	if _, err := os.Stat(working); !os.IsNotExist(err) {
		t.Fatalf("forced remove left the DB: %v", err)
	}
}

func TestPruneOrphanWorktreesOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cacheRoot := filepath.Join(home, "Library", "Caches", "goray")

	aliveRoot := filepath.Join(home, "alive")
	if err := os.MkdirAll(aliveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	orphanRoot := filepath.Join(home, "orphan")
	if err := os.MkdirAll(orphanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeDir := filepath.Join(cacheRoot, "worktrees")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliveDB := filepath.Join(worktreeDir, "alive.db")
	orphanDB := filepath.Join(worktreeDir, "orphan.db")
	makeDBPairAt(t, aliveDB, filepath.Join(cacheRoot, "summaries", "alive-module.db"), aliveRoot)
	makeDBPairAt(t, orphanDB, filepath.Join(cacheRoot, "summaries", "orphan-module.db"), orphanRoot)
	summaryDB := filepath.Join(cacheRoot, "summaries", "shared.db")
	if err := os.MkdirAll(filepath.Dir(summaryDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summaryDB, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(orphanRoot); err != nil {
		t.Fatal(err)
	}

	result, err := Prune(RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, file := range result.Removed {
		removed[file.Path] = true
	}
	if !removed[orphanDB] || result.ReclaimedBytes <= 0 {
		t.Fatalf("prune result = %+v", result)
	}
	if _, err := os.Stat(aliveDB); err != nil {
		t.Fatalf("live worktree DB was removed: %v", err)
	}
	if _, err := os.Stat(summaryDB); err != nil {
		t.Fatalf("prune touched summaries: %v", err)
	}
}

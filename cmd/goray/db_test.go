package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modernsqlite "modernc.org/sqlite"

	"github.com/yanulabs/goray/internal/cache"
	"github.com/yanulabs/goray/internal/index"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("GORAY_CMD_LOCK_DB"); path != "" {
		holdCmdLock(path)
		return
	}
	os.Exit(m.Run())
}

func holdCmdLock(path string) {
	ctx := context.Background()
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=rw"}).String()
	connector, err := modernsqlite.NewConnector(dsn)
	if err != nil {
		os.Exit(3)
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = conn.Close() }()
	stmt, err := conn.Prepare("BEGIN EXCLUSIVE")
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = stmt.Close() }()
	execer, ok := stmt.(driver.StmtExecContext)
	if !ok {
		os.Exit(3)
	}
	if _, err := execer.ExecContext(ctx, nil); err != nil {
		os.Exit(3)
	}
	fmt.Println("LOCKED")
	_, _ = io.ReadAll(os.Stdin)
}

func startCmdLockHolder(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdLockHelper")
	cmd.Env = append(os.Environ(), "GORAY_CMD_LOCK_DB="+path)
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
}

func setupDBModule(t *testing.T, module string) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GORAY_DB", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	working, summary, err := cache.ResolvePaths(root, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.OpenSplit(working, summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background(), &index.Index{RootDir: root, Summary: &index.SummaryData{}}); err != nil {
		_ = c.Close()
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return root, working, summary
}

func addSidecars(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.WriteFile(path+suffix, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func mustNotExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", path, err)
		}
	}
}

func mustExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
	}
}

func runDBCli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(args, &stdout, &stderr, nil)
	return code, stdout.String(), stderr.String()
}

func TestDBRmTargets(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		root, working, summary := setupDBModule(t, "example.com/rmdefault")
		addSidecars(t, working, summary)
		code, out, errOut := runDBCli(t, "db", "rm", root)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if !strings.Contains(out, "removed") || strings.Contains(out, "nothing to remove") {
			t.Fatalf("output = %q", out)
		}
		mustNotExist(t, working, working+"-wal", working+"-shm")
		mustExist(t, summary, summary+"-wal", summary+"-shm")
	})

	t.Run("summaries", func(t *testing.T) {
		root, working, summary := setupDBModule(t, "example.com/rmsummaries")
		addSidecars(t, working, summary)
		code, _, errOut := runDBCli(t, "db", "rm", "--summaries", root)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		mustNotExist(t, summary, summary+"-wal", summary+"-shm")
		mustExist(t, working, working+"-wal", working+"-shm")
	})

	t.Run("all", func(t *testing.T) {
		root, working, summary := setupDBModule(t, "example.com/rmall")
		addSidecars(t, working, summary)
		code, _, errOut := runDBCli(t, "db", "rm", "--all", root)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		mustNotExist(t, working, working+"-wal", working+"-shm", summary, summary+"-wal", summary+"-shm")
	})

	t.Run("dry-run", func(t *testing.T) {
		root, working, summary := setupDBModule(t, "example.com/rmdry")
		addSidecars(t, working)
		code, out, errOut := runDBCli(t, "db", "rm", "--dry-run", root)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		for _, part := range []string{"would remove", working, "bytes"} {
			if !strings.Contains(out, part) {
				t.Fatalf("dry-run output %q missing %q", out, part)
			}
		}
		mustExist(t, working, working+"-wal", working+"-shm", summary)
	})

	t.Run("idempotent", func(t *testing.T) {
		root, working, _ := setupDBModule(t, "example.com/rmmissing")
		if err := os.Remove(working); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runDBCli(t, "db", "rm", root)
		if code != 0 || !strings.Contains(out, "nothing to remove") {
			t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
		}
	})

	t.Run("memory", func(t *testing.T) {
		_, _, _ = setupDBModule(t, "example.com/rmmemory")
		code, _, errOut := runDBCli(t, "db", "rm", "--db", ":memory:")
		if code != 2 || !strings.Contains(errOut, ":memory:") {
			t.Fatalf("exit %d, err %q", code, errOut)
		}
	})

	t.Run("explicit-db", func(t *testing.T) {
		root, working, summary := setupDBModule(t, "example.com/rmexplicit")
		addSidecars(t, working)
		code, out, errOut := runDBCli(t, "db", "rm", "--db", working, root)
		if code != 0 || !strings.Contains(out, "removed") {
			t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
		}
		mustNotExist(t, working, working+"-wal", working+"-shm")
		mustExist(t, summary)
	})
}

func TestDBRmExplicitWithoutModule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GORAY_DB", "")
	noModule := t.TempDir()

	dbPath := filepath.Join(t.TempDir(), "explicit.db")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runDBCli(t, "db", "rm", "--db", dbPath, noModule)
	if code != 0 {
		t.Fatalf("explicit DB outside a module: exit %d: %s", code, errOut)
	}
	mustNotExist(t, dbPath)

	code, _, errOut = runDBCli(t, "db", "rm", "--db", ":memory:", noModule)
	if code != 2 || !strings.Contains(errOut, ":memory:") {
		t.Fatalf(":memory: outside a module: exit %d, err %q", code, errOut)
	}
}

func TestDBRmLockProtection(t *testing.T) {
	root, working, _ := setupDBModule(t, "example.com/rmlocked")
	startCmdLockHolder(t, working)

	code, _, errOut := runDBCli(t, "db", "rm", root)
	if code != 1 || !strings.Contains(errOut, "in use by another process") {
		t.Fatalf("exit %d, err %q", code, errOut)
	}
	mustExist(t, working)

	code, _, errOut = runDBCli(t, "db", "rm", "--force", root)
	if code != 0 {
		t.Fatalf("forced exit %d: %s", code, errOut)
	}
	mustNotExist(t, working)
}

func TestDBPruneCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GORAY_DB", "")

	makeModule := func(module string) (root, working string) {
		root = t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		working, summary, err := cache.ResolvePaths(root, "")
		if err != nil {
			t.Fatal(err)
		}
		c, err := cache.OpenSplit(working, summary)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Sync(context.Background(), &index.Index{RootDir: root, Summary: &index.SummaryData{}}); err != nil {
			_ = c.Close()
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		return root, working
	}

	_, aliveDB := makeModule("example.com/prunealive")
	orphanRoot, orphanDB := makeModule("example.com/pruneorphan")
	summaryRoot, _ := makeModule("example.com/prunesummary")
	_, summaryDB, err := cache.ResolvePaths(summaryRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(orphanRoot); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runDBCli(t, "db", "prune", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "would remove") || !strings.Contains(out, orphanDB) {
		t.Fatalf("prune dry-run output = %q", out)
	}
	mustExist(t, orphanDB, aliveDB)

	code, out, errOut = runDBCli(t, "db", "prune")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, orphanDB) || !strings.Contains(out, "reclaimed bytes:") {
		t.Fatalf("prune output = %q", out)
	}
	mustNotExist(t, orphanDB)
	mustExist(t, aliveDB, summaryDB)
}

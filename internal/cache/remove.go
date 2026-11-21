package cache

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const (
	dbLockTimeout = 2 * time.Second
	canonicalKey  = "canonical_root"
)

// RemovedFile is one file removed (or reported by a dry run) with its size.
type RemovedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size_bytes"`
}

// RemoveOptions controls database removal.
type RemoveOptions struct {
	DryRun bool
	Force  bool
}

// RemoveResult summarizes a removal. Removed is false when nothing existed.
type RemoveResult struct {
	Removed bool          `json:"removed"`
	Files   []RemovedFile `json:"files"`
}

func dbURL(path, query string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: query}).String()
}

// ReadDBMeta reads one cache_meta value without creating or migrating the
// database. Missing tables and keys surface as errors so callers can skip the
// database safely.
func ReadDBMeta(path, key string) (string, error) {
	connector, err := modernsqlite.NewConnector(dbURL(path, "mode=ro"))
	if err != nil {
		return "", err
	}
	db := openReadOnly(connector)
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), dbLockTimeout)
	defer cancel()
	var value string
	err = db.QueryRowContext(ctx, "SELECT value FROM cache_meta WHERE key = ?", key).Scan(&value)
	return value, err
}

func openReadOnly(connector driver.Connector) *sql.DB {
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db
}

func acquireExclusiveLock(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbLockTimeout)
	defer cancel()
	connector, err := modernsqlite.NewConnector(dbURL(path, "mode=rw"))
	if err != nil {
		return err
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	for _, statement := range []string{
		"PRAGMA busy_timeout = 100",
		"PRAGMA locking_mode = EXCLUSIVE",
	} {
		if err := execConn(ctx, conn, statement, nil); err != nil {
			return err
		}
	}
	if err := execConn(ctx, conn, "BEGIN IMMEDIATE", nil); err != nil {
		// A non-SQLite file cannot be held by goray; treat it as removable.
		if strings.Contains(strings.ToLower(err.Error()), "not a database") {
			return nil
		}
		return fmt.Errorf("database %s is in use by another process; stop goray serve or pass --force: %w", path, err)
	}
	_ = execConn(ctx, conn, "ROLLBACK", nil)
	return nil
}

// RemoveDB removes a database and its -wal/-shm sidecars. Without Force it
// first verifies the main file is not exclusively locked by another process,
// avoiding orphaned inodes while goray serve is writing.
func RemoveDB(path string, opts RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Files: []RemovedFile{}}
	if path == "" || path == ":memory:" {
		return result, fmt.Errorf("cannot remove database path %q", path)
	}
	candidates := []string{path, path + "-wal", path + "-shm"}
	existing := make([]RemovedFile, 0, len(candidates))
	mainExists := false
	for i, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, err
		}
		if i == 0 {
			mainExists = true
		}
		existing = append(existing, RemovedFile{Path: candidate, Size: info.Size()})
	}
	if len(existing) == 0 {
		return result, nil
	}
	result.Files = existing
	if opts.DryRun {
		return result, nil
	}
	if !opts.Force && mainExists {
		if err := acquireExclusiveLock(path); err != nil {
			return RemoveResult{}, err
		}
	}
	for _, file := range existing {
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
	}
	result.Removed = true
	return result, nil
}

// SkippedDB records a database the pruner left untouched.
type SkippedDB struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PruneResult summarizes a prune pass.
type PruneResult struct {
	Removed        []RemovedFile `json:"removed"`
	Skipped        []SkippedDB   `json:"skipped"`
	ReclaimedBytes int64         `json:"reclaimed_bytes"`
}

// Prune removes worktree databases whose canonical_root no longer exists. It
// never touches the shared summaries directory. Locked databases and databases
// without metadata are skipped and reported instead of aborting the pass.
func Prune(opts RemoveOptions) (PruneResult, error) {
	result := PruneResult{Removed: []RemovedFile{}, Skipped: []SkippedDB{}}
	base, err := os.UserCacheDir()
	if err != nil {
		return result, err
	}
	pattern := filepath.Join(base, "goray", "worktrees", "*.db")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return result, err
	}
	for _, path := range matches {
		root, err := ReadDBMeta(path, canonicalKey)
		if err != nil {
			reason := "missing canonical_root metadata"
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "locked") || strings.Contains(message, "busy") {
				reason = "database is in use"
			}
			result.Skipped = append(result.Skipped, SkippedDB{Path: path, Reason: reason})
			continue
		}
		if root == "" {
			result.Skipped = append(result.Skipped, SkippedDB{Path: path, Reason: "missing canonical_root metadata"})
			continue
		}
		_, statErr := os.Stat(root)
		if statErr == nil {
			continue
		}
		if !os.IsNotExist(statErr) {
			result.Skipped = append(result.Skipped, SkippedDB{Path: path, Reason: statErr.Error()})
			continue
		}
		removed, err := RemoveDB(path, opts)
		if err != nil {
			result.Skipped = append(result.Skipped, SkippedDB{Path: path, Reason: err.Error()})
			continue
		}
		for _, file := range removed.Files {
			result.ReclaimedBytes += file.Size
			result.Removed = append(result.Removed, file)
		}
	}
	return result, nil
}

// Package cache persists the working index and content-addressed LLM summaries.
package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	PromptVersion  = "v1"
	touchInterval  = 30 * time.Second
	gcInterval     = time.Hour
	gcTTL          = 30 * 24 * time.Hour
	gcCapacity     = 50_000
	schemaFileName = "migrations/*.sql"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Cache owns the SQLite working state and summary cache.
type Cache struct {
	db   *sql.DB
	path string

	touchMu sync.Mutex
	touches map[touchKey]time.Time

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	gcMu   sync.Mutex
	lastGC time.Time
}

type touchKey struct {
	contentHash   string
	promptVersion string
}

// Open configures SQLite and applies embedded migrations.
func Open(path string) (*Cache, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA auto_vacuum = INCREMENTAL",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	c := &Cache{
		db:      db,
		path:    path,
		touches: map[touchKey]time.Time{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.touchLoop()
	return c, nil
}

// DefaultPath returns the per-project cache path outside the repository.
func DefaultPath(projectRoot string) (string, error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	sum := sha256.Sum256([]byte(abs))
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "go-mcp-server", hex.EncodeToString(sum[:])[:16]+".db"), nil
}

// ResolvePath applies flag, env, and default precedence.
func ResolvePath(projectRoot, flagPath string) (string, error) {
	path := flagPath
	if path == "" {
		path = os.Getenv("GO_MCP_DB")
	}
	if path == "" {
		return DefaultPath(projectRoot)
	}
	if path == ":memory:" {
		return path, nil
	}
	return filepath.Abs(path)
}

func migrate(db *sql.DB) error {
	names, err := fs.Glob(migrations, schemaFileName)
	if err != nil {
		return err
	}
	sort.Strings(names)

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for _, name := range names {
		base := filepath.Base(name)
		prefix, _, ok := strings.Cut(base, "_")
		if !ok {
			continue
		}
		next, err := strconv.Atoi(prefix)
		if err != nil || next <= version {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", next)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = next
	}
	return nil
}

// Path is the SQLite database location.
func (c *Cache) Path() string { return c.path }

// Close flushes pending LRU touches and closes SQLite.
func (c *Cache) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.stop)
		<-c.done
		if flushErr := c.FlushTouches(context.Background()); flushErr != nil {
			err = flushErr
		}
		if closeErr := c.db.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	})
	return err
}

func (c *Cache) touchLoop() {
	defer close(c.done)
	ticker := time.NewTicker(touchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = c.FlushTouches(context.Background())
		case <-c.stop:
			return
		}
	}
}

// FlushTouches persists batched last_accessed_at updates.
func (c *Cache) FlushTouches(ctx context.Context) error {
	c.touchMu.Lock()
	pending := c.touches
	c.touches = map[touchKey]time.Time{}
	c.touchMu.Unlock()
	if len(pending) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "UPDATE summary_cache SET last_accessed_at = ? WHERE content_hash = ? AND prompt_version = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for key, accessed := range pending {
		if _, err := stmt.ExecContext(ctx, formatTime(accessed), key.contentHash, key.promptVersion); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	return tx.Commit()
}

func (c *Cache) touch(contentHash string) {
	if contentHash == "" {
		return
	}
	c.touchMu.Lock()
	c.touches[touchKey{contentHash: contentHash, promptVersion: PromptVersion}] = time.Now().UTC()
	c.touchMu.Unlock()
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func nowString() string { return formatTime(time.Now()) }

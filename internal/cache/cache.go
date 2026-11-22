// Package cache persists the working index and content-addressed LLM summaries.
package cache

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// PromptVersion identifies the summary contract used for cache keys.
	PromptVersion = "v1"
	touchInterval = 30 * time.Second
	gcInterval    = time.Hour
	gcTTL         = 30 * 24 * time.Hour
	gcCapacity    = 50_000
)

// Cache owns SQLite working state and the content-addressed summary cache. In
// split mode the working tables live in main and the summary tables live in an
// attached database named summaries.
type Cache struct {
	db            *sql.DB
	path          string
	summaryPath   string
	summarySchema string
	split         bool

	touchMu sync.Mutex
	touches map[touchKey]time.Time

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error

	gcMu   sync.Mutex
	lastGC time.Time

	generation atomic.Uint64
}

// Generation increments whenever stored summaries or working rows change.
// Read-side caches use it to invalidate derived payloads.
func (c *Cache) Generation() uint64 { return c.generation.Load() }

type touchKey struct {
	contentHash   string
	promptVersion string
}

// Open configures a single SQLite database containing every table.
func Open(path string) (*Cache, error) {
	return open(path, path, false)
}

// OpenSplit configures split storage: working tables in workingPath and summary
// tables in summaryPath. Equal paths fall back to single-database mode.
func OpenSplit(workingPath, summaryPath string) (*Cache, error) {
	if workingPath == summaryPath {
		return Open(workingPath)
	}
	return open(workingPath, summaryPath, true)
}

func open(workingPath, summaryPath string, split bool) (*Cache, error) {
	for _, path := range []string{workingPath, summaryPath} {
		if path != ":memory:" {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, err
			}
		}
	}
	db, err := openDB(workingPath, summaryPath, split)
	if err != nil {
		return nil, err
	}
	if err := migrate(db, worktreeMigrations, "migrations/worktree", worktreeScope, ""); err != nil {
		_ = db.Close()
		return nil, err
	}
	schema := ""
	if split {
		schema = "summaries."
	}
	if err := migrate(db, summaryMigrations, "migrations/summary", summaryScope, schema); err != nil {
		_ = db.Close()
		return nil, err
	}

	c := &Cache{
		db:            db,
		path:          workingPath,
		summaryPath:   summaryPath,
		summarySchema: schema,
		split:         split,
		touches:       map[touchKey]time.Time{},
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go c.touchLoop()
	return c, nil
}

// RepoSlug turns a module directory name into a filesystem-friendly label.
func RepoSlug(root string) string {
	base := strings.ToLower(filepath.Base(root))
	var b strings.Builder
	dashed := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			dashed = false
			continue
		}
		if !dashed {
			b.WriteByte('-')
			dashed = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 32 {
		slug = slug[:32]
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "module"
	}
	return slug
}

// Path is the working SQLite database location.
func (c *Cache) Path() string { return c.path }

// SummaryPath is the shared summary SQLite database location. It equals Path
// in single-database mode.
func (c *Cache) SummaryPath() string { return c.summaryPath }

func (c *Cache) summaryTable(name string) string { return c.summarySchema + name }

// stateExpr classifies working symbols, consulting summary tables through the
// store's schema qualifier.
func (c *Cache) stateExpr() string {
	return `CASE
    WHEN w.is_eligible = 0 THEN 'not_eligible'
    WHEN EXISTS (SELECT 1 FROM ` + c.summaryTable("summary_cache") + ` c WHERE c.content_hash = w.content_hash AND c.prompt_version = ?) THEN 'synced'
    WHEN EXISTS (
        SELECT 1 FROM ` + c.summaryTable("symbol_history") + ` h
        JOIN ` + c.summaryTable("summary_cache") + ` c ON c.content_hash = h.last_content_hash AND c.prompt_version = h.prompt_version
        WHERE h.symbol_id = w.id AND h.prompt_version = ? AND c.self_hash = w.self_hash
    ) THEN 'soft_stale'
    WHEN w.self_hash <> '' AND w.kind IN ('func','method','type') AND EXISTS (
        SELECT 1 FROM ` + c.summaryTable("summary_cache") + ` c
        WHERE c.self_hash = w.self_hash AND c.prompt_version = ? AND c.kind = w.kind
    ) THEN 'soft_stale'
    WHEN EXISTS (
        SELECT 1 FROM ` + c.summaryTable("symbol_history") + ` h
        JOIN ` + c.summaryTable("summary_cache") + ` c ON c.content_hash = h.last_content_hash AND c.prompt_version = h.prompt_version
        WHERE h.symbol_id = w.id AND h.prompt_version = ?
    ) THEN 'hard_stale'
    ELSE 'missing'
END`
}

// Close flushes pending LRU touches and closes SQLite.
func (c *Cache) Close() error {
	c.closeOnce.Do(func() {
		close(c.stop)
		<-c.done
		if flushErr := c.FlushTouches(context.Background()); flushErr != nil {
			c.closeErr = flushErr
		}
		if c.path != ":memory:" {
			if checkpointErr := checkpoint(context.Background(), c.db, "main"); checkpointErr != nil && c.closeErr == nil {
				c.closeErr = checkpointErr
			}
		}
		if c.split && c.summaryPath != ":memory:" {
			if checkpointErr := checkpoint(context.Background(), c.db, "summaries"); checkpointErr != nil && c.closeErr == nil {
				c.closeErr = checkpointErr
			}
		}
		if closeErr := c.db.Close(); closeErr != nil && c.closeErr == nil {
			c.closeErr = closeErr
		}
	})
	return c.closeErr
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

// FlushTouches persists batched last_accessed_at updates. It writes only to
// summary_cache.
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
	stmt, err := tx.PrepareContext(ctx, "UPDATE "+c.summaryTable("summary_cache")+
		" SET last_accessed_at = ? WHERE content_hash = ? AND prompt_version = ?")
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

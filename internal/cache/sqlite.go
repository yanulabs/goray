package cache

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	modernsqlite "modernc.org/sqlite"
)

//go:embed migrations/worktree/*.sql
var worktreeMigrations embed.FS

//go:embed migrations/summary/*.sql
var summaryMigrations embed.FS

const (
	worktreeScope = "worktree"
	summaryScope  = "summary"
)

// attachedConnector applies connection-scoped pragmas and the summaries
// attachment on every physical SQLite connection the pool opens. ATTACH is not
// inherited from one pooled connection to the next, so it must live here.
type attachedConnector struct {
	base        driver.Connector
	summaryPath string
	split       bool
}

func (c *attachedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA main.busy_timeout = 5000",
		"PRAGMA main.foreign_keys = ON",
		"PRAGMA main.auto_vacuum = INCREMENTAL",
		"PRAGMA main.journal_mode = WAL",
		"PRAGMA main.synchronous = NORMAL",
	} {
		if err := execConn(ctx, conn, pragma, nil); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if c.split {
		if err := execConn(ctx, conn, "ATTACH DATABASE ? AS summaries",
			[]driver.NamedValue{{Ordinal: 1, Value: c.summaryPath}}); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("attach summary database: %w", err)
		}
		for _, pragma := range []string{
			"PRAGMA summaries.busy_timeout = 5000",
			"PRAGMA summaries.foreign_keys = ON",
			"PRAGMA summaries.auto_vacuum = INCREMENTAL",
			"PRAGMA summaries.journal_mode = WAL",
			"PRAGMA summaries.synchronous = NORMAL",
		} {
			if err := execConn(ctx, conn, pragma, nil); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("%s: %w", pragma, err)
			}
		}
	}
	return conn, nil
}

func (c *attachedConnector) Driver() driver.Driver { return c.base.Driver() }

func execConn(ctx context.Context, conn driver.Conn, query string, args []driver.NamedValue) error {
	if execer, ok := conn.(driver.ExecerContext); ok {
		_, err := execer.ExecContext(ctx, query, args)
		return err
	}
	stmt, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	execer, ok := stmt.(driver.StmtExecContext)
	if !ok {
		return fmt.Errorf("sqlite connection does not support context-aware execution")
	}
	_, err = execer.ExecContext(ctx, args)
	return err
}

func openDB(workingPath, summaryPath string, split bool) (*sql.DB, error) {
	base, err := modernsqlite.NewConnector(workingPath)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(&attachedConnector{
		base:        base,
		summaryPath: summaryPath,
		split:       split,
	})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	return db, nil
}

// migrate applies embedded migrations to one schema scope. Every object name
// is schema-qualified through the {{schema}} placeholder: "" for main in
// single-database mode and "summaries." for the attached summary database.
func migrate(db *sql.DB, migrations fs.FS, dir, scope, schema string) error {
	metaTable := schema + "schema_meta"
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS " + metaTable + ` (
    scope   TEXT PRIMARY KEY,
    version INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("create %s: %w", metaTable, err)
	}

	var version int
	err := db.QueryRow("SELECT version FROM "+metaTable+" WHERE scope = ?", scope).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		version = 0
	} else if err != nil {
		return fmt.Errorf("read %s: %w", metaTable, err)
	}

	names, err := fs.Glob(migrations, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		base := path.Base(name)
		prefix, _, ok := strings.Cut(base, "_")
		if !ok {
			continue
		}
		next, err := strconv.Atoi(prefix)
		if err != nil || next <= version {
			continue
		}
		body, err := fs.ReadFile(migrations, name)
		if err != nil {
			return err
		}
		sqlText := strings.ReplaceAll(string(body), "{{schema}}", schema)
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(sqlText); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.Exec("INSERT INTO "+metaTable+` (scope, version) VALUES (?, ?)
ON CONFLICT(scope) DO UPDATE SET version = excluded.version`, scope, next); err != nil {
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

func checkpoint(ctx context.Context, db *sql.DB, schema string) error {
	if schema == "" {
		schema = "main"
	}
	var busy, logFrames, checkpointed int
	query := "PRAGMA " + schema + ".wal_checkpoint(TRUNCATE)"
	if err := db.QueryRowContext(ctx, query).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("sqlite %s WAL checkpoint is busy", schema)
	}
	return nil
}

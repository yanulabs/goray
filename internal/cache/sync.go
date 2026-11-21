package cache

import (
	"context"
	"fmt"

	"github.com/yanulabs/goray/internal/index"
)

// Sync atomically replaces ephemeral working-tree tables from an index build.
func (c *Cache) Sync(ctx context.Context, idx *index.Index) error {
	if idx == nil || idx.Summary == nil {
		return fmt.Errorf("index has no summary metadata")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO cache_meta (key, value) VALUES ('canonical_root', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, idx.RootDir); err != nil {
		return err
	}

	var generation int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(updated_gen), 0) + 1 FROM working_symbols").Scan(&generation); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO working_symbols (
    id, parent_id, kind, module_name, pkg_path, file_path,
    start_line, end_line, loc, complexity, fan_in,
    is_exported, is_dead, is_eligible,
    sig_hash, self_hash, dep_hash, content_hash, updated_gen
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    parent_id = excluded.parent_id,
    kind = excluded.kind,
    module_name = excluded.module_name,
    pkg_path = excluded.pkg_path,
    file_path = excluded.file_path,
    start_line = excluded.start_line,
    end_line = excluded.end_line,
    loc = excluded.loc,
    complexity = excluded.complexity,
    fan_in = excluded.fan_in,
    is_exported = excluded.is_exported,
    is_dead = excluded.is_dead,
    is_eligible = excluded.is_eligible,
    sig_hash = excluded.sig_hash,
    self_hash = excluded.self_hash,
    dep_hash = excluded.dep_hash,
    content_hash = excluded.content_hash,
    updated_gen = excluded.updated_gen`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, sym := range idx.Summary.Symbols {
		if _, err := stmt.ExecContext(ctx,
			sym.ID, sym.ParentID, sym.Kind, sym.ModuleName, sym.PkgPath, sym.FilePath,
			sym.StartLine, sym.EndLine, sym.LOC, sym.Complexity, sym.FanIn,
			boolInt(sym.Exported), boolInt(sym.Dead), boolInt(sym.Eligible),
			sym.SigHash, sym.SelfHash, sym.DepHash, sym.ContentHash, generation,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM working_symbols WHERE updated_gen != ?", generation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM working_edges"); err != nil {
		return err
	}
	edgeStmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO working_edges (src_id, dst_id, kind) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer func() { _ = edgeStmt.Close() }()
	for _, edge := range idx.Summary.Edges {
		if _, err := edgeStmt.ExecContext(ctx, edge.SrcID, edge.DstID, edge.Kind); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

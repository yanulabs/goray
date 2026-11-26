package cache

import (
	"context"
	"database/sql"
)

// queryRowContext and queryContext count statements issued by the summary
// read path. Report builds use the counter to assert that bulk lookups stay
// O(1) instead of degenerating into per-symbol queries.
func (c *Cache) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.sqlQueries.Add(1)
	return c.db.QueryRowContext(ctx, query, args...)
}

func (c *Cache) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.sqlQueries.Add(1)
	return c.db.QueryContext(ctx, query, args...)
}

// QueryCount returns the number of SQL statements issued by summary lookups.
func (c *Cache) QueryCount() uint64 {
	return c.sqlQueries.Load()
}

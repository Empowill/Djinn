package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// Schema is the version of the database's tables this Djinn writes, kept in the database's user_version. Increment it
// in the change that adds a column to a table, an entity's indexed field included, or a table: TestSchemaPinned fails
// until it is. A Djinn opens a database of its schema or an older one, and raises it. A newer one, written by a newer
// Djinn, it opens too, with a warning, and leaves its schema as it is: entities are protos, whose fields this Djinn
// does not know it keeps and ignores, and the columns it lacks it adds; a migration never stops Djinn (Q42).
const Schema = 1

// Release is the version of this Djinn, such as v0.5.0, which the database records with the schema it raises to, and
// which the warning on a newer database names. djinn sets it at start; empty, it is left out.
var Release string

// schemaTable records, for each schema a Djinn raised the database to, its release and when.
const schemaTable = "store_schema"

// schema reads the database's schema. It never fails: a schema it cannot read, or a database newer than Schema,
// is logged as a warning, and Djinn goes on (Q42).
func (s *Store) schema(ctx context.Context, tx *sql.Tx, path string) int {
	var have int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&have); err != nil {
		logx.L().Warn("database schema unread", "path", path, "err", err)
		return 0
	}
	if have <= Schema {
		return have
	}
	var by string // The release that raised it, when it recorded one.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		schemaTable).Scan(&n); err == nil && n > 0 {
		_ = tx.QueryRowContext(ctx, `SELECT release FROM `+schemaTable+` WHERE version = ?`, have).Scan(&by)
	}
	logx.L().Warn("database written by a newer Djinn: opened anyway, its fields this Djinn does not know ignored",
		"path", path, "schema", have, "by", by, "known", Schema, "release", Release)
	return have
}

// raise marks the database with Schema once its tables are made, recording this release the first time. A database of
// a newer schema keeps its own. raise never fails Open: what it cannot record is a warning (Q42).
func raise(ctx context.Context, tx *sql.Tx, path string, have int) {
	if err := savepoint(ctx, tx, func() error { return mark(ctx, tx, have) }); err != nil {
		logx.L().Warn("database schema not recorded", "path", path, "err", err)
	}
}

func mark(ctx context.Context, tx *sql.Tx, have int) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+schemaTable+` (
		version INTEGER PRIMARY KEY,
		release TEXT NOT NULL,
		at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create the schema table: %w", err)
	}
	if have >= Schema {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+schemaTable+` (version, release, at) VALUES (?, ?, ?)`,
		Schema, Release, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record the schema: %w", err)
	}
	// PRAGMA takes no bound parameter; Schema is a constant.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, Schema)); err != nil {
		return fmt.Errorf("mark the schema: %w", err)
	}
	return nil
}

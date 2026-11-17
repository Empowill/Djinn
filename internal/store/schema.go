package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Schema is the version of the database's tables this Djinn writes, kept in the database's user_version. Increment it
// in the change that adds a column to a table, an entity's indexed field included, or a table: TestSchemaPinned fails
// until it is. A Djinn opens a database of its schema or an older one, and raises it; it refuses a newer one, written by
// a newer Djinn, before writing anything: it would write rows without the columns it does not know, and the newer
// Djinn's indexed queries would then miss them or find them under a stale value (V5 of L08).
const Schema = 1

// Release is the version of this Djinn, such as v0.5.0, which the database records with the schema it raises to, and
// which a refusal names. djinn sets it at start; empty, it is left out.
var Release string

// NewerError is the error of Open on a database whose schema is newer than this Djinn's.
type NewerError struct {
	Path string
	// Have is the database's schema, written by the Djinn By (empty when unknown); Know is this Djinn's, Release.
	Have, Know  int
	By, Release string
}

func (e *NewerError) Error() string {
	by, this, install := "", "", "a newer Djinn"
	if e.By != "" {
		by, install = ", written by Djinn "+e.By, "Djinn "+e.By+" or later"
	}
	if e.Release != "" {
		this = " (" + e.Release + ")"
	}
	where := "the database"
	if e.Path != "" {
		where += " " + e.Path
	}
	return fmt.Sprintf("%s has schema %d%s; this Djinn%s knows schema %d at most and leaves it untouched: "+
		"install %s again, or restore a backup taken before that update (djinn backup restore)",
		where, e.Have, by, this, e.Know, install)
}

// schemaTable records, for each schema a Djinn raised the database to, its release and when.
const schemaTable = "store_schema"

// guard refuses a database newer than Schema, and returns its schema. It runs first in the migration's transaction:
// a refusal writes nothing.
func (s *Store) guard(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	var have int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&have); err != nil {
		return 0, fmt.Errorf("read the schema: %w", err)
	}
	if have <= Schema {
		return have, nil
	}
	e := &NewerError{Path: path, Have: have, Know: Schema, Release: Release}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		schemaTable).Scan(&n); err != nil {
		return 0, fmt.Errorf("read the schema: %w", err)
	}
	if n > 0 {
		err := tx.QueryRowContext(ctx, `SELECT release FROM `+schemaTable+` WHERE version = ?`, have).Scan(&e.By)
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("read the schema: %w", err)
		}
	}
	return have, e
}

// raise marks the database with Schema once its tables are made, recording this release the first time.
func raise(ctx context.Context, tx *sql.Tx, have int) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+schemaTable+` (
		version INTEGER PRIMARY KEY,
		release TEXT NOT NULL,
		at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create the schema table: %w", err)
	}
	if have == Schema {
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

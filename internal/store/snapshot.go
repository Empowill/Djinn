package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// Snapshot writes a consistent copy of the database to dst, which must not exist: VACUUM INTO, in one read
// transaction. Writes wait for it, as they wait for any transaction.
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	return vacuumInto(ctx, s.db, dst)
}

// SnapshotFile writes a consistent copy of the database file src to dst, which must not exist. It neither
// migrates nor writes src, and is safe while a djinn uses it: in WAL mode, a reader never blocks a writer.
func SnapshotFile(ctx context.Context, src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn(src))
	if err != nil {
		return err
	}
	defer db.Close()
	return vacuumInto(ctx, db, dst)
}

// Check opens the database file at path and runs SQLite's integrity check.
func Check(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("check %s: %w", path, err)
	}
	if result != "ok" {
		return fmt.Errorf("check %s: %s", path, result)
	}
	return nil
}

func vacuumInto(ctx context.Context, db *sql.DB, dst string) error {
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("snapshot: %s already exists", dst)
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("snapshot the database: %w", err)
	}
	return nil
}

// dsn opens an existing database file with the settings of Open, without creating it.
func dsn(path string) string {
	q := url.Values{"_pragma": {"busy_timeout(5000)"}, "mode": {"rw"}}
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

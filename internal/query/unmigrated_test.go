package query

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestCollectionsUnmigrated (Q42, W79): questions whose table could not be made at start made the catalog fail, and
// djinn up with it. The catalog is made: a query of the questions fails with ErrUnmigrated, the others read.
func TestCollectionsUnmigrated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), store.File)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	// An index takes the name of the questions' table.
	for _, q := range []string{`CREATE TABLE other (a TEXT)`, `CREATE INDEX question ON other (a)`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := store.Open(ctx, path, plan.Entities()...)
	if err != nil {
		t.Fatalf("Open = %v, want the database opened", err)
	}
	defer s.Close()
	c, err := Collections(s, time.Now)
	if err != nil {
		t.Fatalf("Collections = %v, want the catalog", err)
	}
	if _, err := c.Run(ctx, Query{Collection: "QUESTIONS"}); !errors.Is(err, store.ErrUnmigrated) {
		t.Errorf("query the questions = %v, want ErrUnmigrated", err)
	}
	if _, err := c.Run(ctx, Query{Collection: "WISHES"}); err != nil {
		t.Errorf("query the wishes = %v", err)
	}
}

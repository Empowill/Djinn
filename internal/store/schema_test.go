package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
)

// raw runs statements on the database file at path, without the store.
func raw(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range stmts {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// TestSchema runs alone: its subtests read the global logger (warnings).
func TestSchema(t *testing.T) {
	// made writes a database of this Djinn's schema, with one project, and closes it.
	made := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), File)
		s, err := Open(t.Context(), path, &planv1.Project{})
		if err != nil {
			t.Fatal(err)
		}
		if err := put(t.Context(), s, &planv1.Project{Id: "p1", Name: "one"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("a newer database opens with a warning and keeps its schema (Q42)", func(t *testing.T) {
		path := made(t)
		raw(t, path,
			fmt.Sprintf(`INSERT INTO store_schema (version, release, at) VALUES (%d, 'v9.0.0', '2027-01-01T00:00:00Z')`, Schema+1),
			fmt.Sprintf(`PRAGMA user_version = %d`, Schema+1))
		logs := warnings(t)
		s, err := Open(t.Context(), path, &planv1.Project{}, &planv1.Question{})
		if err != nil {
			t.Fatalf("Open = %v, want the database opened", err)
		}
		defer s.Close()
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Fatal(err)
		}
		if err := put(t.Context(), s, &planv1.Project{Id: "p2", Name: "two"}); err != nil {
			t.Fatal(err)
		}
		if got := strs(t, s, `PRAGMA user_version`); !slices.Equal(got, []string{fmt.Sprint(Schema + 1)}) {
			t.Errorf("user_version = %v, want the newer %d kept", got, Schema+1)
		}
		for _, want := range []string{"database written by a newer Djinn", "by=v9.0.0", fmt.Sprintf("schema=%d", Schema+1)} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log %q lacks %q", logs, want)
			}
		}
	})

	t.Run("a backup of a newer database opens too", func(t *testing.T) {
		path := made(t)
		raw(t, path, fmt.Sprintf(`PRAGMA user_version = %d`, Schema+3))
		copied := filepath.Join(t.TempDir(), "backup.db")
		if err := SnapshotFile(t.Context(), path, copied); err != nil {
			t.Fatal(err)
		}
		logs := warnings(t)
		s, err := Open(t.Context(), copied, &planv1.Project{})
		if err != nil {
			t.Fatalf("Open of the backup = %v", err)
		}
		defer s.Close()
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "database written by a newer Djinn") {
			t.Errorf("log %q lacks the warning", logs)
		}
	})

	t.Run("a schema that cannot be recorded is a warning", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), File)
		// A schema table of another shape: recording this release in it fails.
		raw(t, path, `CREATE TABLE store_schema (other TEXT)`)
		logs := warnings(t)
		s, err := Open(t.Context(), path, &planv1.Project{})
		if err != nil {
			t.Fatalf("Open = %v, want the database opened", err)
		}
		defer s.Close()
		if err := put(t.Context(), s, &planv1.Project{Id: "p1", Name: "one"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "database schema not recorded") {
			t.Errorf("log %q lacks the warning", logs)
		}
	})

	t.Run("a database of this schema opens", func(t *testing.T) {
		path := made(t)
		s, err := Open(t.Context(), path, &planv1.Project{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Fatal(err)
		}
		if got := strs(t, s, `SELECT version FROM store_schema`); !slices.Equal(got, []string{fmt.Sprint(Schema)}) {
			t.Errorf("schemas recorded = %v", got)
		}
	})

	t.Run("an older database opens and is raised", func(t *testing.T) {
		path := made(t)
		// A database of Djinn v0.4.0, before the guard: user_version 0, no schema table.
		raw(t, path, `DROP TABLE store_schema`, `PRAGMA user_version = 0`)
		s, err := Open(t.Context(), path, &planv1.Project{}, &planv1.Question{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Fatal(err)
		}
		if got := strs(t, s, `PRAGMA user_version`); !slices.Equal(got, []string{fmt.Sprint(Schema)}) {
			t.Errorf("user_version = %v, want %d", got, Schema)
		}
		if got := strs(t, s, `SELECT version || ' ' || release FROM store_schema`); !slices.Equal(got, []string{fmt.Sprint(Schema) + " "}) {
			t.Errorf("schemas recorded = %q", got)
		}
	})
}

// warnings captures the warnings logged until the test ends. The logger is global: a test using it runs alone.
func warnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	t.Cleanup(logx.Use(logx.New(&b, slog.LevelWarn)))
	return &b
}

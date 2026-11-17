package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

// files reads every file of dir, by name: the database and what SQLite keeps beside it.
func files(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func TestGuard(t *testing.T) {
	t.Parallel()

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

	t.Run("a newer database is refused, untouched (V5)", func(t *testing.T) {
		t.Parallel()
		path := made(t)
		raw(t, path,
			fmt.Sprintf(`INSERT INTO store_schema (version, release, at) VALUES (%d, 'v9.0.0', '2027-01-01T00:00:00Z')`, Schema+1),
			fmt.Sprintf(`PRAGMA user_version = %d`, Schema+1))
		before := files(t, filepath.Dir(path))
		// The newer database's question table, which this open would create, and the project it would index.
		_, err := Open(t.Context(), path, &planv1.Project{}, &planv1.Question{})
		var newer *NewerError
		if !errors.As(err, &newer) {
			t.Fatalf("Open = %v, want a *NewerError", err)
		}
		if newer.Have != Schema+1 || newer.Know != Schema || newer.By != "v9.0.0" || newer.Path != path {
			t.Errorf("error = %+v", newer)
		}
		for _, want := range []string{
			fmt.Sprintf("has schema %d, written by Djinn v9.0.0", Schema+1),
			fmt.Sprintf("knows schema %d at most", Schema),
			"install Djinn v9.0.0 or later again", "restore a backup",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q lacks %q", err, want)
			}
		}
		after := files(t, filepath.Dir(path))
		if len(after) != len(before) {
			t.Fatalf("files %d after, %d before", len(after), len(before))
		}
		for name, b := range before {
			if !bytes.Equal(after[name], b) {
				t.Errorf("%s changed", name)
			}
		}
	})

	t.Run("a newer database without a release names none", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), File)
		raw(t, path, `CREATE TABLE x (a)`, fmt.Sprintf(`PRAGMA user_version = %d`, Schema+3))
		_, err := Open(t.Context(), path, &planv1.Project{})
		var newer *NewerError
		if !errors.As(err, &newer) || newer.Have != Schema+3 || newer.By != "" {
			t.Fatalf("Open = %v", err)
		}
		if !strings.Contains(err.Error(), "install a newer Djinn again") {
			t.Errorf("message %q", err)
		}
	})

	t.Run("a backup of a newer database is refused too", func(t *testing.T) {
		t.Parallel()
		path := made(t)
		raw(t, path, fmt.Sprintf(`PRAGMA user_version = %d`, Schema+1))
		copied := filepath.Join(t.TempDir(), "backup.db")
		if err := SnapshotFile(t.Context(), path, copied); err != nil {
			t.Fatal(err)
		}
		var newer *NewerError
		if _, err := Open(t.Context(), copied, &planv1.Project{}); !errors.As(err, &newer) {
			t.Fatalf("Open of the backup = %v, want a *NewerError", err)
		}
	})

	t.Run("a database of this schema opens", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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

func TestNewerErrorError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		e    NewerError
		want string
	}{
		{"all known", NewerError{Path: "/d/djinn.db", Have: 3, Know: 2, By: "v0.7.0", Release: "v0.6.0"},
			"the database /d/djinn.db has schema 3, written by Djinn v0.7.0; this Djinn (v0.6.0) knows schema 2 at most " +
				"and leaves it untouched: install Djinn v0.7.0 or later again, or restore a backup taken before that update " +
				"(djinn backup restore)"},
		{"nothing known", NewerError{Have: 3, Know: 2},
			"the database has schema 3; this Djinn knows schema 2 at most and leaves it untouched: install a " +
				"newer Djinn again, or restore a backup taken before that update (djinn backup restore)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.e.Error(); got != c.want {
				t.Errorf("Error() =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

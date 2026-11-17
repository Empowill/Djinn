package store

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestMigrationStep runs alone: its subtests read the global logger (warnings). Each makes one step of the migration
// fail on a database left so by hand, and checks that the store opens with a warning, that the part the step makes
// fails each use with ErrUnmigrated, and that the other parts read and write (Q42: a warning, never a stop).
func TestMigrationStep(t *testing.T) {
	// opened opens the database at path with the projects and the questions, wanting it opened with a warning that
	// names part.
	opened := func(t *testing.T, path, part string, entities ...proto.Message) *Store {
		t.Helper()
		logs := warnings(t)
		s, err := Open(t.Context(), path, append([]proto.Message{&planv1.Project{}, &planv1.Question{}}, entities...)...)
		if err != nil {
			t.Fatalf("Open = %v, want the database opened", err)
		}
		t.Cleanup(func() { s.Close() })
		for _, want := range []string{"database migration step failed", part} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log %q lacks %q", logs, want)
			}
		}
		return s
	}
	unmigrated := func(t *testing.T, what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrUnmigrated) {
			t.Errorf("%s = %v, want ErrUnmigrated", what, err)
		}
	}
	// works checks that the questions read and write, and that the journal reads.
	works := func(t *testing.T, s *Store) {
		t.Helper()
		ctx := t.Context()
		if err := put(ctx, s, &planv1.Question{Id: "q1", WishId: "w1"}); err != nil {
			t.Fatalf("put a question = %v", err)
		}
		if _, err := Get[*planv1.Question](ctx, s, "q1"); err != nil {
			t.Fatalf("get a question = %v", err)
		}
		if _, err := Commands(ctx, s, nil); err != nil {
			t.Errorf("Commands = %v", err)
		}
	}
	// made writes a database with one project, and closes it.
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
	projectsFail := func(t *testing.T, s *Store) {
		t.Helper()
		_, err := Get[*planv1.Project](t.Context(), s, "p1")
		unmigrated(t, "Get of a project", err)
		_, err = List[*planv1.Project](t.Context(), s, nil)
		unmigrated(t, "List of the projects", err)
		unmigrated(t, "Put of a project", put(t.Context(), s, &planv1.Project{Id: "p2", Name: "two"}))
	}

	t.Run("an entity whose table cannot be made", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), File)
		// An index takes the name of the projects' table.
		raw(t, path, `CREATE TABLE other (a TEXT)`, `CREATE INDEX project ON other (a)`)
		s := opened(t, path, "entity plan.v1.Project")
		projectsFail(t, s)
		works(t, s)
	})

	t.Run("an entity whose column cannot be added", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), File)
		v1, v2 := thing(t, 1), thing(t, 2)
		s, err := Open(ctx, path, dynamicpb.NewMessage(v1))
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		// A row whose payload cannot be read: the column v2 adds cannot be filled from it.
		raw(t, path, `INSERT INTO thing (id, payload) VALUES ('t1', x'ff')`)
		s = opened(t, path, "entity test.v1.Thing", dynamicpb.NewMessage(v2))
		_, err = s.table(v2)
		unmigrated(t, "the things' table", err)
		thing := dynamicpb.NewMessage(v2)
		thing.Set(v2.Fields().ByName("id"), protoreflect.ValueOfString("t2"))
		unmigrated(t, "Put of a thing", put(ctx, s, thing))
		// The step is undone whole: the column added before the fill is gone.
		if cols := strs(t, s, `SELECT name FROM pragma_table_info('thing')`); slices.Contains(cols, "owner_id") {
			t.Errorf("thing's columns = %v, want owner_id undone", cols)
		}
		works(t, s)
		if err := put(ctx, s, &planv1.Project{Id: "p1", Name: "one"}); err != nil {
			t.Errorf("put a project = %v", err)
		}
	})

	t.Run("an entity whose index cannot be made, made at the next start", func(t *testing.T) {
		path := made(t)
		// Two projects of the same name, written without the unique index.
		raw(t, path, `DROP INDEX project_unique_name`,
			`INSERT INTO project (id, payload, name) SELECT 'p2', payload, name FROM project WHERE id = 'p1'`)
		s := opened(t, path, "entity plan.v1.Project")
		projectsFail(t, s)
		works(t, s)
		s.Close()

		// Once the duplicate is gone, the next start makes the index, and the projects read again.
		raw(t, path, `DELETE FROM project WHERE id = 'p2'`)
		logs := warnings(t)
		s, err := Open(t.Context(), path, &planv1.Project{}, &planv1.Question{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Errorf("get a project after the fix = %v", err)
		}
		if logs.Len() > 0 {
			t.Errorf("warnings after the fix: %s", logs)
		}
	})

	t.Run("the journal", func(t *testing.T) {
		path := made(t)
		raw(t, path, `DROP TABLE command`, `CREATE TABLE other (a TEXT)`, `CREATE INDEX command ON other (a)`)
		s := opened(t, path, partJournal)
		// The entities read; no change can be journaled.
		if _, err := Get[*planv1.Project](t.Context(), s, "p1"); err != nil {
			t.Errorf("get a project = %v", err)
		}
		unmigrated(t, "Put of a project", put(t.Context(), s, &planv1.Project{Id: "p2", Name: "two"}))
		_, err := Commands(t.Context(), s, nil)
		unmigrated(t, "Commands", err)
		_, err = CommandsOf(t.Context(), s, "/", nil)
		unmigrated(t, "CommandsOf", err)
		if _, err := s.Samples(t.Context(), "machine", time.Time{}); err != nil {
			t.Errorf("Samples = %v", err)
		}
	})

	t.Run("the samples", func(t *testing.T) {
		path := made(t)
		raw(t, path, `DROP TABLE sample`, `CREATE TABLE other (a TEXT)`, `CREATE INDEX sample ON other (a)`)
		s := opened(t, path, partSamples)
		ctx := t.Context()
		unmigrated(t, "AddSamples", s.AddSamples(ctx, []Sample{{Series: "machine", At: time.Now()}}))
		_, err := s.Samples(ctx, "machine", time.Time{})
		unmigrated(t, "Samples", err)
		_, _, err = s.LastSample(ctx, "machine")
		unmigrated(t, "LastSample", err)
		unmigrated(t, "PruneSamples", s.PruneSamples(ctx, time.Now(), Retention{}))
		works(t, s)
		if err := s.AddInterval(ctx, Interval{Released: time.Now(), Data: []byte("x")}); err != nil {
			t.Errorf("AddInterval = %v", err)
		}
	})

	t.Run("the gate intervals", func(t *testing.T) {
		path := made(t)
		raw(t, path, `DROP TABLE gate_interval`, `CREATE TABLE other (a TEXT)`, `CREATE INDEX gate_interval ON other (a)`)
		s := opened(t, path, partIntervals)
		ctx := t.Context()
		unmigrated(t, "AddInterval", s.AddInterval(ctx, Interval{Released: time.Now(), Data: []byte("x")}))
		_, err := s.Intervals(ctx, time.Time{})
		unmigrated(t, "Intervals", err)
		unmigrated(t, "PruneIntervals", s.PruneIntervals(ctx, time.Now()))
		works(t, s)
		if err := s.AddSamples(ctx, []Sample{{Series: "machine", At: time.Now()}}); err != nil {
			t.Errorf("AddSamples = %v", err)
		}
	})
}

package store

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	t.Run("creates schema and indexes", func(t *testing.T) {
		t.Parallel()
		s := open(t)
		tables := strs(t, s, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if want := []string{"command", "project", "question", "sample"}; !slices.Equal(tables, want) {
			t.Errorf("tables = %v, want %v", tables, want)
		}
		if cols, want := strs(t, s, `SELECT name FROM pragma_table_info('question')`), []string{"id", "payload", "wish_id", "task_id", "about_task_id", "code"}; !slices.Equal(cols, want) {
			t.Errorf("question columns = %v, want %v", cols, want)
		}
		if cols, want := strs(t, s, `SELECT name FROM pragma_table_info('project')`), []string{"id", "payload", "name", "directory"}; !slices.Equal(cols, want) {
			t.Errorf("project columns = %v, want %v (git and create_time stay in the payload)", cols, want)
		}
		indexes := strs(t, s, `SELECT name FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL ORDER BY name`)
		want := []string{
			"command_method", "project_unique_directory", "project_unique_name", "question_about_task_id", "question_task_id", "question_unique_wish_id_code", "question_wish_id",
			"sample_at", "sample_series_at",
		}
		if !slices.Equal(indexes, want) {
			t.Errorf("indexes = %v, want %v", indexes, want)
		}
		if cols := strs(t, s, `SELECT name FROM pragma_table_info('command')`); !slices.Equal(cols, []string{"seq", "id", "actor", "at", "method", "request"}) {
			t.Errorf("command columns = %v", cols)
		}
	})

	t.Run("duplicate entity table rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := Open(t.Context(), "", &planv1.Project{}, &planv1.Project{}); err == nil {
			t.Fatal("expected error when the same entity table is registered twice")
		}
	})

	t.Run("entity without string id rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := Open(t.Context(), "", &timestamppb.Timestamp{}); err == nil {
			t.Fatal("expected error when entity has no string id")
		}
	})
}

func TestGet(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	api := &planv1.Project{Id: NewID(), Name: "Api", Directory: "/src/api", Git: true}
	if err := put(ctx, s, api); err != nil {
		t.Fatal(err)
	}

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		got, err := Get[*planv1.Project](ctx, s, api.GetId())
		if err != nil || !proto.Equal(got, api) {
			t.Fatalf("Get = %v, %v; want %v", got, err, api)
		}
	})

	t.Run("unknown id returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		if _, err := Get[*planv1.Project](ctx, s, NewID()); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get of an unknown id: %v, want ErrNotFound", err)
		}
	})

	t.Run("unregistered entity type returns error", func(t *testing.T) {
		t.Parallel()
		if _, err := Get[*planv1.Wish](ctx, s, api.GetId()); err == nil {
			t.Error("Get of a type that is not an entity: no error")
		}
	})
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	api := &planv1.Project{Id: NewID(), Name: "Api", Directory: "/src/api", Git: true}
	web := &planv1.Project{Id: NewID(), Name: "web", Directory: "/src/web"}
	if err := put(ctx, s, api, web); err != nil {
		t.Fatal(err)
	}

	t.Run("all oldest first", func(t *testing.T) {
		t.Parallel()
		all, err := List[*planv1.Project](ctx, s, nil)
		if err != nil || len(all) != 2 || all[0].GetName() != "Api" || all[1].GetName() != "web" {
			t.Errorf("List = %v, %v; want api then web", all, err)
		}
	})

	t.Run("where ignores case", func(t *testing.T) {
		t.Parallel()
		found, err := List[*planv1.Project](ctx, s, Where{"name": "API"})
		if err != nil || len(found) != 1 || found[0].GetId() != api.GetId() {
			t.Errorf("List by name ignoring case = %v, %v", found, err)
		}
	})

	t.Run("unindexed field rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := List[*planv1.Project](ctx, s, Where{"git": true}); err == nil {
			t.Error("List by a field that is not indexed: no error")
		}
	})
}

func TestListAfter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.TaskEvent{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Written out of order: ListAfter reads them by seq.
	for _, seq := range []int64{3, 1, 4, 2} {
		ev := &planv1.TaskEvent{Id: NewID(), TaskId: "t", Seq: seq, Text: fmt.Sprintf("event %d", seq)}
		if seq%2 == 0 {
			ev.Kind = planv1.TaskEventKind_TASK_EVENT_KIND_GATE
		}
		if err := put(ctx, s, ev, &planv1.TaskEvent{Id: NewID(), TaskId: "other", Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	seqs := func(t *testing.T, after After) []int64 {
		t.Helper()
		events, err := ListAfter[*planv1.TaskEvent](ctx, s, Where{"task_id": "t"}, after)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, ev := range events {
			out = append(out, ev.GetSeq())
		}
		return out
	}

	t.Run("ordered by seq past From", func(t *testing.T) {
		t.Parallel()
		if got := seqs(t, After{Field: "seq", From: 1}); !slices.Equal(got, []int64{2, 3, 4}) {
			t.Errorf("after 1 = %v, want 2 3 4", got)
		}
	})

	t.Run("filtered by payload Contains", func(t *testing.T) {
		t.Parallel()
		gate, err := proto.Marshal(&planv1.TaskEvent{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE})
		if err != nil {
			t.Fatal(err)
		}
		if got := seqs(t, After{Field: "seq", Contains: [][]byte{gate, []byte("event")}}); !slices.Equal(got, []int64{2, 4}) {
			t.Errorf("gate events = %v, want 2 4", got)
		}
	})

	t.Run("text field rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := ListAfter[*planv1.TaskEvent](ctx, s, nil, After{Field: "task_id"}); err == nil {
			t.Error("ListAfter on a text field: no error")
		}
	})
}

func TestLatest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	var ids []string
	for _, name := range []string{"a", "b", "c", "d"} {
		p := &planv1.Project{Id: NewID(), Name: name, Directory: "/src/" + name}
		ids = append(ids, p.GetId())
		if err := put(ctx, s, p); err != nil {
			t.Fatal(err)
		}
	}
	// b put again keeps its place: the last written first is d.
	if err := put(ctx, s, &planv1.Project{Id: ids[1], Name: "b2", Directory: "/src/b"}); err != nil {
		t.Fatal(err)
	}

	t.Run("last written first and stops when f returns false", func(t *testing.T) {
		t.Parallel()
		var seen []string
		err := Latest(ctx, s, nil, func(p *planv1.Project) bool { seen = append(seen, p.GetName()); return len(seen) < 3 })
		if err != nil || !slices.Equal(seen, []string{"d", "c", "b2"}) {
			t.Errorf("Latest = %v, %v; want d, c, b2 and no further", seen, err)
		}
	})

	t.Run("filtered by where ignoring case", func(t *testing.T) {
		t.Parallel()
		var seen []string
		err := Latest(ctx, s, Where{"name": "A"}, func(p *planv1.Project) bool { seen = append(seen, p.GetName()); return true })
		if err != nil || !slices.Equal(seen, []string{"a"}) {
			t.Errorf("Latest by name = %v, %v", seen, err)
		}
	})
}

func TestTx(t *testing.T) {
	t.Parallel()

	t.Run("mutations without Journal fail", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		p := &planv1.Project{Id: NewID(), Name: "api", Directory: "/src/api"}
		for _, tc := range []struct {
			name string
			fn   func(*Tx) error
		}{
			{name: "Put", fn: func(tx *Tx) error { return tx.Put(p) }},
			{name: "Delete", fn: func(tx *Tx) error { return tx.Delete(p) }},
			{name: "Change", fn: func(tx *Tx) error { return tx.Change(p) }},
		} {
			if err := s.Tx(ctx, tc.fn); err == nil {
				t.Errorf("%s without Journal: no error", tc.name)
			}
		}
	})

	t.Run("rollback leaves neither entity nor journal", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		p := &planv1.Project{Id: NewID(), Name: "api", Directory: "/src/api"}
		boom := errors.New("boom")
		err := s.Tx(ctx, func(tx *Tx) error {
			if err := tx.Journal("lead", "/plan.v1.ProjectService/Add", &planv1.ProjectServiceAddRequest{Directory: "/src/api"}); err != nil {
				return err
			}
			if err := tx.Put(p); err != nil {
				return err
			}
			// The transaction reads what it wrote.
			if _, err := Get[*planv1.Project](ctx, tx, p.GetId()); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Tx = %v, want boom", err)
		}
		if n := strs(t, s, `SELECT count(*) FROM command`); n[0] != "0" {
			t.Errorf("a rolled back command is in the journal: %s rows", n[0])
		}
		if _, err := Get[*planv1.Project](ctx, s, p.GetId()); !errors.Is(err, ErrNotFound) {
			t.Errorf("a rolled back entity is stored: %v", err)
		}
	})

	t.Run("commit writes journal, replaces by id, deletes and notifies OnCommit", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		var committed [][]proto.Message
		s.OnCommit(func(ms []proto.Message) {
			committed = append(committed, slices.Clone(ms))
		})

		p := &planv1.Project{Id: NewID(), Name: "api", Directory: "/src/api"}
		req := &planv1.ProjectServiceAddRequest{Directory: "/src/api", Name: "api"}
		var jid string
		if err := s.Tx(ctx, func(tx *Tx) error {
			if err := tx.Journal("lead", "/plan.v1.ProjectService/Add", req); err != nil {
				return err
			}
			jid = tx.JournalID()
			return tx.Put(p)
		}); err != nil {
			t.Fatal(err)
		}
		if len(jid) != 36 {
			t.Errorf("JournalID = %q", jid)
		}

		var seq int64
		var id, actor, at, method string
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT seq, id, actor, at, method, request FROM command`).
			Scan(&seq, &id, &actor, &at, &method, &raw); err != nil {
			t.Fatal(err)
		}
		got := &planv1.ProjectServiceAddRequest{}
		if err := proto.Unmarshal(raw, got); err != nil || !proto.Equal(got, req) {
			t.Errorf("journaled request = %v, %v; want %v", got, err, req)
		}
		if seq != 1 || id != jid || actor != "lead" || at == "" || method != "/plan.v1.ProjectService/Add" {
			t.Errorf("command = %d %q %q %q %q", seq, id, actor, at, method)
		}

		// Put replaces by id.
		p.Name = "api-v2"
		if err := put(ctx, s, p); err != nil {
			t.Fatal(err)
		}
		if gotP, _ := Get[*planv1.Project](ctx, s, p.GetId()); gotP.GetName() != "api-v2" {
			t.Errorf("after a second Put, name = %q", gotP.GetName())
		}

		// Change and Delete report to OnCommit and remove the entity.
		q := &planv1.Question{Id: NewID(), WishId: "w1", Code: "Q01"}
		if err := s.Tx(ctx, func(tx *Tx) error {
			if err := tx.Journal("lead", "/plan.v1.ProjectService/Delete", req); err != nil {
				return err
			}
			if err := tx.Change(q); err != nil {
				return err
			}
			return tx.Delete(&planv1.Project{Id: p.GetId()})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := Get[*planv1.Project](ctx, s, p.GetId()); !errors.Is(err, ErrNotFound) {
			t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
		}
		if len(committed) != 3 || len(committed[2]) != 2 {
			t.Errorf("OnCommit notifications = %d (last batch %d), want 3 (last batch 2)", len(committed), len(committed[len(committed)-1]))
		}
	})
}

func TestCommands(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	for _, method := range []string{"/plan.v1.ProjectService/Add", "harness/event", "/plan.v1.ProjectService/Delete"} {
		if err := s.Tx(ctx, func(tx *Tx) error {
			return tx.Journal("test", method, &planv1.ProjectServiceListRequest{})
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := Commands(ctx, s, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("Commands(nil) = %d, %v; want 3", len(all), err)
	}
	filtered, err := Commands(ctx, s, func(c Command) bool { return c.Seq == 2 })
	if err != nil || len(filtered) != 1 || filtered[0].Method != "harness/event" {
		t.Errorf("Commands(Seq==2) = %v, %v", filtered, err)
	}
}

func TestCommandsOf(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	for _, method := range []string{"/plan.v1.ProjectService/Add", "harness/event", "/plan.v1.ProjectService/Delete", "harness/end"} {
		if err := s.Tx(ctx, func(tx *Tx) error { return tx.Journal("test", method, &planv1.ProjectServiceListRequest{}) }); err != nil {
			t.Fatal(err)
		}
	}
	methods := func(cs []Command) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Method)
		}
		return out
	}

	for _, tc := range []struct {
		name   string
		prefix string
		keep   func(Command) bool
		want   []string
	}{
		{
			name:   "API prefix",
			prefix: "/",
			want:   []string{"/plan.v1.ProjectService/Add", "/plan.v1.ProjectService/Delete"},
		},
		{
			name:   "prefix with keep filter",
			prefix: "harness/",
			keep:   func(c Command) bool { return c.Seq > 2 },
			want:   []string{"harness/end"},
		},
		{
			name:   "empty prefix returns all",
			prefix: "",
			want:   []string{"/plan.v1.ProjectService/Add", "harness/event", "/plan.v1.ProjectService/Delete", "harness/end"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CommandsOf(ctx, s, tc.prefix, tc.keep)
			if err != nil || !slices.Equal(methods(got), tc.want) {
				t.Errorf("CommandsOf(%q) = %v, %v; want %v", tc.prefix, methods(got), err, tc.want)
			}
		})
	}
}

package query

import (
	"errors"
	"slices"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// codes runs q selecting code alone, and gives the codes of its rows.
func codes(t *testing.T, c *Catalog, q Query) []string {
	t.Helper()
	q.Selects = []string{"code"}
	table, err := c.Run(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(table.Rows))
	for _, r := range table.Rows {
		s, _ := r[0].(string)
		out = append(out, s)
	}
	return out
}

func TestRun(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	ctx := t.Context()

	t.Run("values typed, null and not 0", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Selects: []string{
			"code", "status", "usage.cost_usd", "usage.input_tokens", "create_time", "end_time", "running",
			"urgent", "depends_on", "project_id", "integration.state",
		}})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]any{
			{"W1", "TASK_STATUS_DONE", 1.5, int64(100), day0, day0.Add(2 * time.Hour), 90 * time.Minute, false,
				nil, id(1), "INTEGRATION_STATE_COMMITTED"},
			{"W2", "TASK_STATUS_RUNNING", 0.0, int64(0), day0.Add(24 * time.Hour), nil, nil, false,
				[]any{id(4)}, id(1), nil},
			{"W1", nil, nil, nil, day0.Add(48 * time.Hour), nil, nil, true, nil, nil, nil},
			{"W3", "TASK_STATUS_FAILED", 0.25, int64(0), day0.Add(time.Hour), nil, nil, false, nil, id(1), nil},
		}
		if table.Total != 4 || len(table.Rows) != 4 {
			t.Fatalf("rows %d, total %d; want 4", len(table.Rows), table.Total)
		}
		for i, row := range want {
			for j, v := range row {
				if !same(table.Rows[i][j], v) {
					t.Errorf("row %d %s = %#v; want %#v", i, table.Columns[j].Ref, table.Rows[i][j], v)
				}
			}
		}
		if table.Columns[2].Type != TypeFloat || table.Columns[4].Type != TypeTime || !table.Columns[8].List {
			t.Errorf("columns typed %v", table.Columns)
		}
	})

	t.Run("jumps", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Selects: []string{
			"code", "wish_id->title", "wish_id->state", "project_id->name", "part_of->code",
			"wait_question_id->answer.choice", "part_of->wish_id->title",
		}})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]any{
			{"W1", "Stats", "WISH_STATE_ACTIVE", "djinn", nil, nil, nil},
			{"W2", "Stats", "WISH_STATE_ACTIVE", "djinn", "W1", "CHOICE_A", "Stats"},
			{"W1", "Docs", "WISH_STATE_GRANTED", nil, nil, nil, nil},
			{"W3", nil, nil, "djinn", nil, nil, nil},
		}
		for i, row := range want {
			for j, v := range row {
				if !same(table.Rows[i][j], v) {
					t.Errorf("row %d %s = %#v; want %#v", i, table.Columns[j].Ref, table.Rows[i][j], v)
				}
			}
		}
		if col := table.Columns[2]; col.Ref != "wish_id->state" || col.Type != TypeEnum ||
			col.Field.Enum().FullName() != "plan.v1.WishState" {
			t.Errorf("wish_id->state column = %+v; want the WishState enum", col)
		}
	})

	t.Run("questions jump to tasks", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "QUESTIONS", Selects: []string{"code", "task_id->title"},
			Filters: []Filter{IsNull{Ref: "task_id->title"}}})
		if err != nil || len(table.Rows) != 1 || table.Rows[0][0] != "Q2" {
			t.Errorf("questions asked by no task = %v, %v; want Q2", table, err)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		var refs []string
		for _, col := range table.Columns {
			refs = append(refs, col.Ref)
		}
		if !slices.Equal(refs, []string{"code", "title", "status"}) {
			t.Errorf("default columns = %v", refs)
		}
		table, err = c.Run(ctx, Query{Collection: "WISHES"})
		if err != nil || len(table.Columns) != len(Schema((&planv1.Wish{}).ProtoReflect().Descriptor())) {
			t.Errorf("WISHES without defaults: %v, %v; want every ref of the schema", table, err)
		}
	})

	t.Run("sorts, nulls last", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name  string
			sorts []Sort
			want  []string
		}{
			{"source order", nil, []string{"W1", "W2", "W1", "W3"}},
			{"cost", []Sort{{Ref: "usage.cost_usd"}}, []string{"W2", "W3", "W1", "W1"}},
			{"cost desc", []Sort{{Ref: "usage.cost_usd", Desc: true}}, []string{"W1", "W3", "W2", "W1"}},
			{"status by number", []Sort{{Ref: "status"}}, []string{"W2", "W1", "W3", "W1"}},
			{"code then create time desc", []Sort{{Ref: "code"}, {Ref: "create_time", Desc: true}},
				[]string{"W1", "W1", "W2", "W3"}},
			{"wish title", []Sort{{Ref: "wish_id->title"}}, []string{"W1", "W1", "W2", "W3"}},
			{"title ignores case", []Sort{{Ref: "title"}}, []string{"W2", "W3", "W1", "W1"}},
			{"urgent", []Sort{{Ref: "urgent", Desc: true}}, []string{"W1", "W1", "W2", "W3"}},
			{"running", []Sort{{Ref: "running"}}, []string{"W1", "W2", "W1", "W3"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if got := codes(t, c, Query{Collection: "TASKS", Sorts: tc.sorts}); !slices.Equal(got, tc.want) {
					t.Errorf("codes = %v; want %v", got, tc.want)
				}
			})
		}
		table, err := c.Run(ctx, Query{Collection: "TASKS", Selects: []string{"title"},
			Sorts: []Sort{{Ref: "code"}, {Ref: "create_time", Desc: true}}})
		if err != nil || table.Rows[0][0] != "docs page" {
			t.Errorf("code then create time desc: first %v, %v; want docs page", table.Rows[0], err)
		}
	})

	t.Run("limit keeps the total", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Limit: 2, Sorts: []Sort{{Ref: "code", Desc: true}}})
		if err != nil || table.Total != 4 || len(table.Rows) != 2 || table.Rows[0][0] != "W3" {
			t.Errorf("limit 2 = %v, %v; want W3 first, total 4", table, err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			q    Query
		}{
			{"no collection", Query{Collection: "NOPE"}},
			{"negative limit", Query{Collection: "TASKS", Limit: -1}},
			{"hidden select", Query{Collection: "TASKS", Selects: []string{"prompt"}}},
			{"twice", Query{Collection: "TASKS", Selects: []string{"code", "code"}}},
			{"sort on a list", Query{Collection: "TASKS", Sorts: []Sort{{Ref: "depends_on"}}}},
			{"sort on nothing", Query{Collection: "TASKS", Sorts: []Sort{{Ref: "nope"}}}},
			{"nil filter", Query{Collection: "TASKS", Filters: []Filter{nil}}},
			{"bad filter", Query{Collection: "TASKS", Filters: []Filter{Equal{Ref: "status", Value: "LOST"}}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if _, err := c.Run(ctx, tc.q); !errors.Is(err, ErrInvalid) {
					t.Errorf("Run = %v; want ErrInvalid", err)
				}
			})
		}
	})
}

func TestPushdown(t *testing.T) {
	t.Parallel()
	c, tasks := catalog(t)
	for _, tc := range []struct {
		name    string
		filters []Filter
		where   map[string]string
		want    []string
	}{
		{"wish id", []Filter{Equal{Ref: "wish_id", Value: id(3)}}, map[string]string{"wish_id": id(3)},
			[]string{"W1"}},
		{"code, ignoring case", []Filter{Equal{Ref: "code", Value: "w2"}}, map[string]string{"code": "w2"},
			[]string{"W2"}},
		{"two values for one field", []Filter{Equal{Ref: "code", Value: "W1"}, Equal{Ref: "code", Value: "W2"}},
			map[string]string{"code": "W1"}, []string{}},
		{"not a column", []Filter{Equal{Ref: "title", Value: "Aggregates"}}, map[string]string{},
			[]string{"W2"}},
		{"inside a group", []Filter{Group{Op: And, Filters: []Filter{Equal{Ref: "project_id", Value: id(1)}}}},
			map[string]string{}, []string{"W1", "W2", "W3"}},
		{"a jump", []Filter{Equal{Ref: "wish_id->title", Value: "docs"}}, map[string]string{}, []string{"W1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Serial: the spy keeps the where of every load of TASKS.
			tasks.mu.Lock()
			tasks.wheres = nil
			tasks.mu.Unlock()
			got := codes(t, c, Query{Collection: "TASKS", Filters: tc.filters})
			if !slices.Equal(got, tc.want) {
				t.Errorf("codes = %v; want %v", got, tc.want)
			}
			tasks.mu.Lock()
			defer tasks.mu.Unlock()
			if len(tasks.wheres) != 1 || !maps(tasks.wheres[0], tc.where) {
				t.Errorf("loads where %v; want %v", tasks.wheres, tc.where)
			}
		})
	}
}

func TestNewCatalog(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	tasks, questions := c.byName["TASKS"].Source, c.byName["QUESTIONS"].Source
	for _, tc := range []struct {
		name string
		cols []Collection
		err  string
	}{
		{"no name", []Collection{{Source: tasks}}, "a name and a source"},
		{"no source", []Collection{{Name: "TASKS"}}, "a name and a source"},
		{"a name twice", []Collection{{Name: "A", Source: tasks}, {Name: "A", Source: questions}}, "A twice"},
		{"a type twice", []Collection{{Name: "A", Source: tasks}, {Name: "B", Source: tasks}}, "already"},
		{"a default hidden", []Collection{{Name: "A", Source: tasks, Defaults: []string{"prompt"}}}, "hidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCatalog(tc.cols...); !contains(err, tc.err) {
				t.Errorf("NewCatalog = %v; want %q", err, tc.err)
			}
		})
	}
}

func TestCatalogSchema(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	cols, err := c.Schema("QUESTIONS")
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, col := range cols {
		refs[col.Ref] = true
	}
	if !refs["text"] || !refs["answer.choice"] || refs["context"] {
		t.Errorf("QUESTIONS refs = %v; want text and answer.choice, never context", refs)
	}
	if _, err := c.Schema("NOPE"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Schema(NOPE) = %v; want ErrInvalid", err)
	}
}

func TestFromStore(t *testing.T) {
	t.Parallel()
	s := openFrozen(t)
	src, err := FromStore(s, &planv1.Question{})
	if err != nil {
		t.Fatal(err)
	}
	// Text columns only: id, the *_id fields and the unique code.
	if got := src.Indexed(); !slices.Contains(got, "wish_id") || !slices.Contains(got, "code") {
		t.Errorf("Indexed = %v", got)
	}
	msgs, err := src.Load(t.Context(), map[string]string{"wish_id": id(2)})
	if err != nil || len(msgs) != 1 || msgs[0].(*planv1.Question).GetCode() != "Q1" {
		t.Errorf("Load where wish Stats = %v, %v; want Q1", msgs, err)
	}
	if _, err := FromStore(s, &planv1.Block{}); err == nil {
		t.Error("FromStore of a type the store does not hold: no error")
	}
}

// same compares two values of a row: times by Equal, lists by their items.
func same(a, b any) bool {
	switch x := a.(type) {
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	case []any:
		y, ok := b.([]any)
		return ok && slices.Equal(x, y)
	}
	return a == b
}

func maps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

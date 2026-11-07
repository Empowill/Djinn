package query

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

func TestDerived(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	ctx := t.Context()

	t.Run("tasks", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Selects: []string{
			"code", "prompt::size", "integration::delay", "wait_question_id->answer::delay",
		}})
		if err != nil {
			t.Fatal(err)
		}
		// T1: 13 letters, merged 10 s after its end; T2 waits on Q1, answered after an hour.
		rows(t, table, [][]any{
			{"W1", int64(4), 10 * time.Second, nil},
			{"W2", nil, nil, time.Hour},
			{"W1", nil, nil, nil},
			{"W3", nil, nil, nil},
		})
		_, types := names(table)
		if !slices.Equal(types, []Type{TypeString, TypeInt, TypeDuration, TypeDuration}) {
			t.Errorf("types %v", types)
		}
		if col := table.Columns[1]; col.Field != nil || !strings.Contains(col.Doc, "divided by 4") {
			t.Errorf("prompt::size column = %+v", col)
		}
	})

	t.Run("questions", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "QUESTIONS", Selects: []string{
			"code", "context::size", "answer.note::size", "answer::delay",
		}})
		if err != nil {
			t.Fatal(err)
		}
		rows(t, table, [][]any{{"Q1", int64(2), int64(3), time.Hour}, {"Q2", int64(2), nil, nil}})
	})

	t.Run("filter, sort and measure", func(t *testing.T) {
		t.Parallel()
		if got := codes(t, c, Query{Collection: "TASKS", Filters: []Filter{Range{Ref: "prompt::size", Gte: 1}}}); !slices.Equal(got, []string{"W1"}) {
			t.Errorf("prompt::size >= 1: %v; want W1", got)
		}
		if got := codes(t, c, Query{Collection: "QUESTIONS", Filters: []Filter{IsNull{Ref: "answer::delay"}}}); !slices.Equal(got, []string{"Q2"}) {
			t.Errorf("answer::delay null: %v; want Q2", got)
		}
		if got := codes(t, c, Query{Collection: "TASKS", Sorts: []Sort{{Ref: "integration::delay"}}}); got[0] != "W1" {
			t.Errorf("sorted by integration::delay: %v; want W1 first", got)
		}
		table, err := c.Run(ctx, Query{Collection: "QUESTIONS", Aggregation: &Aggregation{
			By:       []GroupBy{{Ref: "wish_id->title"}},
			Measures: []Measure{{Ref: "answer::delay", Fn: Median}, {Ref: "context::size", Fn: Sum}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if refs, _ := names(table); !slices.Equal(refs, []string{"wish_id->title", "answer::delay::median", "context::size::sum"}) {
			t.Errorf("columns %v", refs)
		}
		rows(t, table, [][]any{{"Docs", nil, int64(2)}, {"Stats", time.Hour, int64(2)}})
	})

	t.Run("in the schema, never what they hide", func(t *testing.T) {
		t.Parallel()
		cols, err := c.Schema("TASKS")
		if err != nil {
			t.Fatal(err)
		}
		refs := map[string]bool{}
		for _, col := range cols {
			refs[col.Ref] = true
		}
		if !refs["prompt::size"] || !refs["integration::delay"] || refs["prompt"] {
			t.Errorf("TASKS refs: prompt::size %v, integration::delay %v, prompt %v", refs["prompt::size"],
				refs["integration::delay"], refs["prompt"])
		}
	})
}

func TestDerivedValues(t *testing.T) {
	t.Parallel()
	at := func(h time.Duration) *timestamppb.Timestamp {
		return timestamppb.New(day0.Add(h))
	}
	committed := func(update *timestamppb.Timestamp) *planv1.TaskIntegration {
		return &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, UpdateTime: update}
	}
	for _, tc := range []struct {
		name string
		refs []Derived
		ref  string
		m    proto.Message
		want any
	}{
		{"size rounds up", TaskRefs, "prompt::size", &planv1.Task{Prompt: "abcde"}, int64(2)},
		{"size counts letters, not bytes", TaskRefs, "prompt::size", &planv1.Task{Prompt: strings.Repeat(string(rune(0xe9)), 4)}, int64(1)},
		{"no prompt is null", TaskRefs, "prompt::size", &planv1.Task{}, nil},
		{"delay of a commit", TaskRefs, "integration::delay",
			&planv1.Task{EndTime: at(0), Integration: committed(at(time.Minute))}, time.Minute},
		{"a commit at its end is 0", TaskRefs, "integration::delay",
			&planv1.Task{EndTime: at(0), Integration: committed(at(0))}, time.Duration(0)},
		{"not committed is null", TaskRefs, "integration::delay", &planv1.Task{EndTime: at(0),
			Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_RED, UpdateTime: at(1)}}, nil},
		{"no end is null", TaskRefs, "integration::delay", &planv1.Task{Integration: committed(at(0))}, nil},
		{"a commit before its end is null", TaskRefs, "integration::delay",
			&planv1.Task{EndTime: at(time.Hour), Integration: committed(at(0))}, nil},
		{"no answer is null", QuestionRefs, "answer::delay", &planv1.Question{CreateTime: at(0)}, nil},
		{"an answer without time is null", QuestionRefs, "answer::delay",
			&planv1.Question{CreateTime: at(0), Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_A}}, nil},
		{"answer delay", QuestionRefs, "answer::delay",
			&planv1.Question{CreateTime: at(0), Answer: &planv1.Answer{CreateTime: at(90 * time.Second)}}, 90 * time.Second},
		{"another message is null", QuestionRefs, "context::size", &planv1.Task{Prompt: "abcd"}, nil},
		{"a task without kind is work", TaskRefs, "type", &planv1.Task{}, "TASK_TYPE_WORK"},
		{"work", TaskRefs, "type", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_WORK}, "TASK_TYPE_WORK"},
		{"a question worker is work", TaskRefs, "type", &planv1.Task{Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR},
			"TASK_TYPE_WORK"},
		{"correction", TaskRefs, "type", &planv1.Task{Correction: &planv1.TaskCorrection{}}, "TASK_TYPE_CORRECTION"},
		{"review", TaskRefs, "type", &planv1.Task{Review: &planv1.TaskReview{}}, "TASK_TYPE_REVIEW"},
		{"investigation", TaskRefs, "type", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION},
			"TASK_TYPE_INVESTIGATION"},
		{"mockup", TaskRefs, "type", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_MOCKUP}, "TASK_TYPE_MOCKUP"},
		{"ifrit", TaskRefs, "type", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_IFRIT}, "TASK_TYPE_IFRIT"},
		{"watcher, by its provider", TaskRefs, "type", &planv1.Task{Provider: planv1.Provider_PROVIDER_WATCH},
			"TASK_TYPE_WATCHER"},
		{"azima", TaskRefs, "type", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_AZIMA}, "TASK_TYPE_AZIMA"},
		{"type of another message is null", TaskRefs, "type", &planv1.Question{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			i := slices.IndexFunc(tc.refs, func(d Derived) bool { return d.Ref == tc.ref })
			if i < 0 {
				t.Fatalf("no derived ref %s", tc.ref)
			}
			if got := tc.refs[i].get(tc.m); got != tc.want {
				t.Errorf("%s = %#v; want %#v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestDerivedDeclared checks every declared derived ref: it reads its collection's message, its name does not hide a
// field, and its values have its type.
func TestDerivedDeclared(t *testing.T) {
	t.Parallel()
	full := map[string]proto.Message{
		"plan.v1.Task": &planv1.Task{Prompt: "p", EndTime: timestamppb.New(day0),
			Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
				UpdateTime: timestamppb.New(day0)}},
		"plan.v1.Question": &planv1.Question{Context: "c", CreateTime: timestamppb.New(day0),
			Answer: &planv1.Answer{Note: "n", CreateTime: timestamppb.New(day0)}},
	}
	for _, refs := range [][]Derived{TaskRefs, QuestionRefs} {
		for _, d := range refs {
			m, ok := full[string(d.of)]
			if !ok {
				t.Fatalf("%s reads %s: no message in this test", d.Ref, d.of)
			}
			if err := d.check(m.ProtoReflect().Descriptor()); err != nil {
				t.Error(err)
			}
			// A value of the task itself, such as its type, reads no one field: its name holds no ::.
			if d.Doc == "" || (!strings.Contains(d.Ref, "::") && d.Type != TypeEnum) {
				t.Errorf("%s: a doc and a name with :: each", d.Ref)
			}
			v := d.get(m)
			var ok2 bool
			switch d.Type {
			case TypeInt:
				_, ok2 = v.(int64)
			case TypeDuration:
				_, ok2 = v.(time.Duration)
			case TypeEnum:
				s, _ := v.(string)
				ok2 = d.Enum != nil && d.Enum.Values().ByName(protoreflect.Name(s)) != nil
			}
			if !ok2 {
				t.Errorf("%s = %#v; want a %s", d.Ref, v, d.Type)
			}
		}
	}
}

func TestNewCatalogDerived(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	tasks := c.byName["TASKS"].Source
	size := func(t *planv1.Task) any { return nil }
	for _, tc := range []struct {
		name string
		d    []Derived
		err  string
	}{
		{"another message", QuestionRefs[:1], "reads plan.v1.Question"},
		{"twice", []Derived{TaskRefs[0], TaskRefs[0]}, "twice"},
		{"a field's name", []Derived{Derive("usage.cost_usd", TypeFloat, "d", size)}, "a field's ref"},
		{"a jump", []Derived{Derive("wish_id->x", TypeInt, "d", size)}, "without ->"},
		{"no name", []Derived{Derive("", TypeInt, "d", size)}, "without ->"},
		{"an enum without its values", []Derived{Derive("x::y", TypeEnum, "d", size)}, "an enum"},
		{"values of an int", []Derived{func() Derived {
			d := DeriveEnum("x::y", "d", taskType)
			d.Type = TypeInt
			return d
		}()}, "an enum"},
		{"no type", []Derived{Derive("x::y", 0, "d", size)}, "no type"},
		{"not by Derive", []Derived{{Ref: "x::y", Type: TypeInt}}, "not declared by Derive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCatalog(Collection{Name: "TASKS", Source: tasks, Derived: tc.d}); !contains(err, tc.err) {
				t.Errorf("NewCatalog = %v; want %q", err, tc.err)
			}
		})
	}
}

// TestTimed reads the times of the tasks as the task list computes them, which the store does not keep, without
// writing to the store.
func TestTimed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	at := func(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(day0.Add(d)) }
	run := func(from, to time.Duration) *planv1.TaskRun {
		return &planv1.TaskRun{StartTime: at(from), EndTime: at(to)}
	}
	wish, azima := id(1), id(2)
	done := planv1.TaskStatus_TASK_STATUS_DONE
	gate := func(task string, seq int64, text string, when time.Duration) *planv1.TaskEvent {
		return &planv1.TaskEvent{Id: id(int(10 + seq)), TaskId: task, Seq: seq, Text: text, CreateTime: at(when),
			Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE}
	}
	msgs := []proto.Message{
		&planv1.Task{Id: azima, WishId: wish, Code: "W1", Kind: planv1.TaskKind_TASK_KIND_AZIMA, CreateTime: at(0)},
		// W2: two runs of an hour an hour apart, 10 min of gates kept, started 30 min after its creation.
		&planv1.Task{Id: id(3), WishId: wish, Code: "W2", Status: done, PartOf: azima, CreateTime: at(-30 * time.Minute),
			FirstStartTime: at(0), StartTime: at(2 * time.Hour), EndTime: at(3 * time.Hour),
			Runs: []*planv1.TaskRun{run(0, time.Hour), run(2*time.Hour, 3*time.Hour)}},
		&planv1.GateWait{Id: id(3), Waited: durationpb.New(10 * time.Minute)},
		// W3: from before GateWait, its 5 min of gate counted from its events, and not kept.
		&planv1.Task{Id: id(4), WishId: wish, Code: "W3", Status: done, PartOf: azima, CreateTime: at(0),
			StartTime: at(0), EndTime: at(time.Hour), Runs: []*planv1.TaskRun{run(0, time.Hour)}},
		gate(id(4), 1, "gate test: asked", 10*time.Minute),
		gate(id(4), 2, "gate test: taken", 15*time.Minute),
		// W4: never started, planned for 5 hours now.
		&planv1.Task{Id: id(5), WishId: wish, Code: "W4", CreateTime: at(0)},
	}
	err = s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "/test.v1.TestService/Put", &planv1.ProjectServiceListRequest{}); err != nil {
			return err
		}
		for _, m := range msgs {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := FromStore(s, &planv1.Task{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCatalog(Collection{Name: "TASKS", Derived: TaskRefs,
		Source: Timed(s, src, func() time.Time { return day0.Add(5 * time.Hour) })})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("rows", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Selects: []string{
			"code", "type", "running", "waiting_gate", "paused", "waiting_before_start", "part_of->running",
		}})
		if err != nil {
			t.Fatal(err)
		}
		rows(t, table, [][]any{
			// An azima's times sum its parts'.
			{"W1", "TASK_TYPE_AZIMA", 165 * time.Minute, 15 * time.Minute, time.Hour, 30 * time.Minute, nil},
			{"W2", "TASK_TYPE_WORK", 110 * time.Minute, 10 * time.Minute, time.Hour, 30 * time.Minute, 165 * time.Minute},
			{"W3", "TASK_TYPE_WORK", 55 * time.Minute, 5 * time.Minute, time.Duration(0), time.Duration(0),
				165 * time.Minute},
			{"W4", "TASK_TYPE_WORK", nil, nil, nil, 5 * time.Hour, nil},
		})
		if _, err := store.Get[*planv1.GateWait](ctx, s, id(4)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("W3's gate wait = %v; want none kept: a query only reads", err)
		}
	})

	t.Run("measure and bins", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Filters: []Filter{Equal{Ref: "type", Value: "work"}},
			Aggregation: &Aggregation{
				By:       []GroupBy{{Ref: "running", Bounds: []any{"0s", "1h", "2h"}}},
				Measures: []Measure{{Fn: Count}, {Ref: "paused", Fn: Sum}},
			}})
		if err != nil {
			t.Fatal(err)
		}
		rows(t, table, [][]any{
			{time.Duration(0), int64(1), time.Duration(0)},
			{time.Hour, int64(1), time.Hour},
			{2 * time.Hour, int64(0), nil},
			{nil, int64(1), nil},
		})
	})

	t.Run("by type", func(t *testing.T) {
		t.Parallel()
		table, err := c.Run(ctx, Query{Collection: "TASKS", Aggregation: &Aggregation{
			By: []GroupBy{{Ref: "type"}}, Measures: []Measure{{Fn: Count}, {Ref: "running", Fn: Sum}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		rows(t, table, [][]any{{"TASK_TYPE_WORK", int64(3), 165 * time.Minute}, {"TASK_TYPE_AZIMA", int64(1), 165 * time.Minute}})
		var b strings.Builder
		if err := table.WriteCSV(&b); err != nil {
			t.Fatal(err)
		}
		if want := "type,count,running::sum\nwork,3,2h45m\nazima,1,2h45m\n"; b.String() != want {
			t.Errorf("csv:\n%s\nwant:\n%s", b.String(), want)
		}
	})
}

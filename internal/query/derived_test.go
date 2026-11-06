package query

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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
			if d.Doc == "" || !strings.Contains(d.Ref, "::") {
				t.Errorf("%s: a doc and a name with :: each", d.Ref)
			}
			v := d.get(m)
			var ok2 bool
			switch d.Type {
			case TypeInt:
				_, ok2 = v.(int64)
			case TypeDuration:
				_, ok2 = v.(time.Duration)
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
		{"an enum", []Derived{Derive("x::y", TypeEnum, "d", size)}, "an enum"},
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

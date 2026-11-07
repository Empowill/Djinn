package querytext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
)

const (
	tasks     = queryv1.Collection_COLLECTION_TASKS
	wishes    = queryv1.Collection_COLLECTION_WISHES
	text      = queryv1.ColumnType_COLUMN_TYPE_STRING
	enum      = queryv1.ColumnType_COLUMN_TYPE_ENUM
	float     = queryv1.ColumnType_COLUMN_TYPE_FLOAT
	integer   = queryv1.ColumnType_COLUMN_TYPE_INT
	instant   = queryv1.ColumnType_COLUMN_TYPE_TIME
	duration  = queryv1.ColumnType_COLUMN_TYPE_DURATION
	boolean   = queryv1.ColumnType_COLUMN_TYPE_BOOL
	notExists = "nope"
)

// types reads a small schema: the tasks' refs, and the wishes' across wish_id; it counts its reads.
func types(reads *int) *Types {
	cols := map[queryv1.Collection][]*queryv1.QueryColumn{
		tasks: {
			{Ref: "code", Type: text}, {Ref: "title", Type: text}, {Ref: "status", Type: enum},
			{Ref: "usage.cost_usd", Type: float}, {Ref: "prompt::size", Type: integer},
			{Ref: "create_time", Type: instant}, {Ref: "running", Type: duration}, {Ref: "urgent", Type: boolean},
			{Ref: "wish_id", Type: text, Jump: wishes},
		},
		wishes: {{Ref: "title", Type: text}, {Ref: "project_ids", Type: text, List: true}},
	}
	return NewTypes(func(_ context.Context, c queryv1.Collection) ([]*queryv1.QueryColumn, error) {
		*reads++
		return cols[c], nil
	})
}

func filter(t *testing.T, s string) *queryv1.Filter {
	t.Helper()
	var reads int
	f, err := Filter(t.Context(), s, tasks, types(&reads))
	if err != nil {
		t.Fatalf("Filter(%q): %v", s, err)
	}
	return f
}

func TestFilter(t *testing.T) {
	t.Parallel()
	day := timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local))
	for _, tt := range []struct {
		in   string
		want *queryv1.Filter
	}{
		{"status=running", str("status", queryv1.StringOp_STRING_OP_EQUAL, "running")},
		{"wish_id->title=Stats", str("wish_id->title", queryv1.StringOp_STRING_OP_EQUAL, "Stats")},
		{"status=done|failed", in("status", false, "done", "failed")},
		{"status=null", isNull("status")},
		{"status=pending|null", in("status", true, "pending")},
		{"status!=done", not(str("status", queryv1.StringOp_STRING_OP_EQUAL, "done"))},
		{"status!=null", not(isNull("status"))},
		{"usage.cost_usd>1", &queryv1.Filter{Where: &queryv1.Filter_Number{Number: &queryv1.NumberComparison{
			Ref: "usage.cost_usd", Op: queryv1.CompareOp_COMPARE_OP_GREATER_THAN, Value: 1}}}},
		{"prompt::size<=-1", &queryv1.Filter{Where: &queryv1.Filter_Number{Number: &queryv1.NumberComparison{
			Ref: "prompt::size", Op: queryv1.CompareOp_COMPARE_OP_LESS_THAN_EQUAL, Value: -1}}}},
		{"create_time>=2026-11-01", &queryv1.Filter{Where: &queryv1.Filter_Time{Time: &queryv1.TimeComparison{
			Ref: "create_time", Op: queryv1.CompareOp_COMPARE_OP_GREATER_THAN_EQUAL, Value: day}}}},
		{"running<90s", &queryv1.Filter{Where: &queryv1.Filter_Duration{Duration: &queryv1.DurationComparison{
			Ref: "running", Op: queryv1.CompareOp_COMPARE_OP_LESS_THAN, Value: durationpb.New(90 * time.Second)}}}},
		{"running=1h|null", &queryv1.Filter{Where: &queryv1.Filter_Group{Group: &queryv1.FilterGroup{
			Op: queryv1.FilterGroupOp_FILTER_GROUP_OP_OR, Filters: []*queryv1.Filter{
				{Where: &queryv1.Filter_Duration{Duration: &queryv1.DurationComparison{
					Ref: "running", Op: queryv1.CompareOp_COMPARE_OP_EQUAL, Value: durationpb.New(time.Hour)}}},
				isNull("running"),
			}}}}},
		{"urgent=true", &queryv1.Filter{Where: &queryv1.Filter_Bool{Bool: &queryv1.BoolComparison{
			Ref: "urgent", Value: true}}}},
		{"title*=a=b|c", str("title", queryv1.StringOp_STRING_OP_CONTAINS, "a=b|c")},
		{"code^=W", str("code", queryv1.StringOp_STRING_OP_BEGINS_WITH, "W")},
		{"wish_id->project_ids=P1", str("wish_id->project_ids", queryv1.StringOp_STRING_OP_EQUAL, "P1")},
	} {
		if got := filter(t, tt.in); !proto.Equal(got, tt.want) {
			t.Errorf("Filter(%q) = %v; want %v", tt.in, got, tt.want)
		}
	}
	for _, tt := range []struct{ in, want string }{
		{"", "no ref"}, {"=x", "no ref"}, {"status", "no operator"}, {"status running", "no operator"},
		{"status=", "no value"}, {"status~x", "no operator"}, {notExists + "=x", "no ref nope"},
		{"running>soon", "is not a duration"}, {"usage.cost_usd=a", "is not a number"},
		{"create_time<monday", "is not a time"}, {"title>S", "> >= < <= bound"}, {"running*=1", "not a text"},
		{"urgent>true", "compared by = only"}, {"title->x=1", "no id to read"},
	} {
		var reads int
		_, err := Filter(t.Context(), tt.in, tasks, types(&reads))
		if !errors.Is(err, ErrInvalid) || !contains(err.Error(), tt.want) {
			t.Errorf("Filter(%q) = %v; want ErrInvalid with %q", tt.in, err, tt.want)
		}
	}
}

// TestTypesReadEachSchemaOnce: the filters of one command read the schema of a collection once.
func TestTypesReadEachSchemaOnce(t *testing.T) {
	t.Parallel()
	var reads int
	ty := types(&reads)
	for _, s := range []string{"status=done", "code=W1", "wish_id->title=x", "wish_id->project_ids=y"} {
		if _, err := Filter(t.Context(), s, tasks, ty); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 2 {
		t.Errorf("%d schema reads; want 2, the tasks' then the wishes'", reads)
	}
}

func TestOrderGroupMeasure(t *testing.T) {
	t.Parallel()
	if o, err := Order("-create_time"); err != nil || !proto.Equal(o, &queryv1.Order{Ref: "create_time", Desc: true}) {
		t.Errorf("Order(-create_time) = %v, %v", o, err)
	}
	if _, err := Order("-"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Order(-) = %v; want ErrInvalid", err)
	}
	for _, tt := range []struct{ in, want string }{
		{"status", "status"},
		{"create_time::week", "create_time::week"},
		{"wish_id->title::month", "wish_id->title::month"},
		{"running::bins(0s, 10s,1m)", "running::bins(0s,10s,1m0s)"},
		{"prompt::size::bins(0,100,1e3)", "prompt::size::bins(0,100,1000)"},
		{"running", "running"},
	} {
		var reads int
		g, err := Group(t.Context(), tt.in, tasks, types(&reads))
		if err != nil || GroupText(g) != tt.want {
			t.Errorf("Group(%q) = %v (%s), %v; want %s", tt.in, g, GroupText(g), err, tt.want)
		}
	}
	for _, tt := range []struct{ in, want string }{
		{"", "an empty group"}, {"running::bins(1s,2s", "write ref::bins"}, {"running::bins(1s,,2s)", "an empty bound"},
		{"::bins(1s,2s)", "write ref::bins"}, {"code::bins(a,b)", "has no bins"}, {"running::bins(1,2)", "not a duration"},
		{"prompt::size::bins(1s)", "not a number"},
	} {
		var reads int
		if _, err := Group(t.Context(), tt.in, tasks, types(&reads)); !errors.Is(err, ErrInvalid) ||
			!contains(err.Error(), tt.want) {
			t.Errorf("Group(%q) = %v; want ErrInvalid with %q", tt.in, err, tt.want)
		}
	}
	for _, tt := range []struct{ in, want string }{
		{"count", "count"}, {"n=count", "n=count"}, {"running::median", "running::median"},
		{"answer::delay::p90", "answer::delay::p90"}, {"cost = usage.cost_usd::sum", "cost=usage.cost_usd::sum"},
		{"wish_id::count", "wish_id::count"},
	} {
		if m, err := Measure(tt.in); err != nil || MeasureText(m) != tt.want {
			t.Errorf("Measure(%q) = %v, %v; want %s", tt.in, m, err, tt.want)
		}
	}
	for _, in := range []string{"", "running", "running::mean", "::sum", "=count", "x::unspecified"} {
		if _, err := Measure(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Measure(%q) = %v; want ErrInvalid", in, err)
		}
	}
}

// TestFilterText: a filter's text reads back as the same filter.
func TestFilterText(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"status=running", "status=done|failed", "status=null", "status=pending|null", "status!=done",
		"status!=null", "usage.cost_usd>1.5", "running<1m30s", "running=1h0m0s|null", "urgent=false", "title*=x",
		"code^=W",
	} {
		var reads int
		f, err := Filter(t.Context(), in, tasks, types(&reads))
		if err != nil {
			t.Fatal(err)
		}
		back, err := Filter(t.Context(), FilterText(f), tasks, types(&reads))
		if err != nil || !proto.Equal(f, back) {
			t.Errorf("%q -> %q -> %v, %v", in, FilterText(f), back, err)
		}
	}
	if got := OrderText(&queryv1.Order{Ref: "count", Desc: true}); got != "-count" {
		t.Errorf("OrderText = %q", got)
	}
}

func str(ref string, op queryv1.StringOp, v string) *queryv1.Filter {
	return &queryv1.Filter{Where: &queryv1.Filter_String_{String_: &queryv1.StringComparison{Ref: ref, Op: op, Value: v}}}
}

func in(ref string, withNull bool, values ...string) *queryv1.Filter {
	return &queryv1.Filter{Where: &queryv1.Filter_StringIn{StringIn: &queryv1.StringIn{
		Ref: ref, Values: values, IncludeNull: withNull}}}
}

func isNull(ref string) *queryv1.Filter {
	return &queryv1.Filter{Where: &queryv1.Filter_Null{Null: &queryv1.NullFilter{Ref: ref}}}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

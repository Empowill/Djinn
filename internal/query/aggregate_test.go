package query

import (
	"errors"
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Paris on any machine, Windows included.
)

// aggregate runs an aggregation of TASKS with filters, and checks its columns' names and types.
func aggregate(t *testing.T, c *Catalog, a Aggregation, filters ...Filter) *Table {
	t.Helper()
	if a.Location == nil {
		a.Location = time.UTC
	}
	table, err := c.Run(t.Context(), Query{Collection: "TASKS", Aggregation: &a, Filters: filters})
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// rows checks a table's rows against want, value by value.
func rows(t *testing.T, table *Table, want [][]any) {
	t.Helper()
	if len(table.Rows) != len(want) {
		t.Fatalf("rows = %v; want %v", table.Rows, want)
	}
	for i, row := range want {
		for j, v := range row {
			if !same(table.Rows[i][j], v) {
				t.Errorf("row %d %s = %#v; want %#v", i, table.Columns[j].Ref, table.Rows[i][j], v)
			}
		}
	}
}

// names gives the refs of a table's columns, then their types.
func names(table *Table) ([]string, []Type) {
	var refs []string
	var types []Type
	for _, c := range table.Columns {
		refs = append(refs, c.Ref)
		types = append(types, c.Type)
	}
	return refs, types
}

func TestAggregate(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	nov := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }

	t.Run("by day, an empty period never shows, null and not 0", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{
			By:       []GroupBy{{Ref: "create_time", Granularity: Day}},
			Measures: []Measure{{Fn: Count}, {Ref: "usage.cost_usd", Fn: Sum}, {Ref: "usage.cost_usd", Fn: Count}},
		})
		refs, types := names(table)
		if !slices.Equal(refs, []string{"create_time::day", "count", "usage.cost_usd::sum", "usage.cost_usd::count"}) ||
			!slices.Equal(types, []Type{TypeTime, TypeInt, TypeFloat, TypeInt}) {
			t.Errorf("columns %v %v", refs, types)
		}
		if col := table.Columns[0]; col.Of != "create_time" || col.Granularity != Day || col.Field == nil {
			t.Errorf("group column = %+v", col)
		}
		if col := table.Columns[2]; col.Of != "usage.cost_usd" || col.Fn != Sum {
			t.Errorf("measure column = %+v", col)
		}
		// T3, alone on the 4th, has no usage: its sum is null, its count of costs 0.
		rows(t, table, [][]any{
			{nov(2, 0), int64(2), 1.75, int64(2)},
			{nov(3, 0), int64(1), 0.0, int64(1)},
			{nov(4, 0), int64(1), nil, int64(0)},
		})
		if table.Total != 3 {
			t.Errorf("total = %d; want 3 groups", table.Total)
		}
	})

	t.Run("a day starts at midnight where the machine is", func(t *testing.T) {
		t.Parallel()
		// At UTC-10, 09:00 UTC is 23:00 the day before.
		loc := time.FixedZone("UTC-10", -10*3600)
		table := aggregate(t, c, Aggregation{Location: loc,
			By: []GroupBy{{Ref: "create_time", Granularity: Day}}, Measures: []Measure{{Fn: Count}}})
		rows(t, table, [][]any{
			{nov(1, 10), int64(1)}, {nov(2, 10), int64(2)}, {nov(3, 10), int64(1)},
		})
	})

	t.Run("weeks start on Monday, months on the 1st", func(t *testing.T) {
		t.Parallel()
		// 2 November 2026 is a Monday: in UTC, the four tasks fall in its week; at UTC-10, T1 falls on Sunday.
		table := aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "create_time", Granularity: Week}},
			Measures: []Measure{{Fn: Count}}})
		rows(t, table, [][]any{{nov(2, 0), int64(4)}})
		loc := time.FixedZone("UTC-10", -10*3600)
		table = aggregate(t, c, Aggregation{Location: loc, By: []GroupBy{{Ref: "create_time", Granularity: Week}},
			Measures: []Measure{{Fn: Count}}})
		rows(t, table, [][]any{
			{time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC), int64(1)}, {nov(2, 10), int64(3)},
		})
		table = aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "create_time", Granularity: Month}},
			Measures: []Measure{{Fn: Count}}})
		rows(t, table, [][]any{{nov(1, 0), int64(4)}})
		if table.Columns[0].Ref != "create_time::month" {
			t.Errorf("column %s; want create_time::month", table.Columns[0].Ref)
		}
	})

	t.Run("by enum, in its numbers' order, null last", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "status"}},
			Measures: []Measure{{Fn: Count}, {Ref: "running", Fn: Median}}})
		rows(t, table, [][]any{
			{"TASK_STATUS_RUNNING", int64(1), nil},
			{"TASK_STATUS_DONE", int64(1), 90 * time.Minute},
			{"TASK_STATUS_FAILED", int64(1), nil},
			{nil, int64(1), nil},
		})
		if table.Columns[2].Type != TypeDuration {
			t.Errorf("running::median is a %s; want a duration", table.Columns[2].Type)
		}
	})

	t.Run("two groups, across a jump", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "wish_id->title"}, {Ref: "code"}},
			Measures: []Measure{{Fn: Count, Alias: "tasks"}}})
		refs, _ := names(table)
		if !slices.Equal(refs, []string{"wish_id->title", "code", "tasks"}) {
			t.Errorf("columns %v", refs)
		}
		rows(t, table, [][]any{
			{"Docs", "W1", int64(1)}, {"Stats", "W1", int64(1)}, {"Stats", "W2", int64(1)}, {nil, "W3", int64(1)},
		})
	})

	t.Run("filters first, then the whole set is one row", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{Measures: []Measure{
			{Fn: Count}, {Ref: "usage.cost_usd", Fn: Sum}, {Ref: "usage.cost_usd", Fn: Avg},
			{Ref: "usage.cost_usd", Fn: Min}, {Ref: "usage.cost_usd", Fn: Max}, {Ref: "create_time", Fn: Max},
			{Ref: "usage.input_tokens", Fn: Avg}, {Ref: "usage.input_tokens", Fn: Sum},
		}}, IsNull{Ref: "end_time"})
		rows(t, table, [][]any{{int64(3), 0.25, 0.125, 0.0, 0.25, nov(4, 9), 0.0, int64(0)}})
		_, types := names(table)
		if !slices.Equal(types, []Type{TypeInt, TypeFloat, TypeFloat, TypeFloat, TypeFloat, TypeTime, TypeFloat, TypeInt}) {
			t.Errorf("types %v", types)
		}
		table = aggregate(t, c, Aggregation{Measures: []Measure{{Fn: Count}, {Ref: "running", Fn: Sum},
			{Ref: "running", Fn: P90}}}, Equal{Ref: "code", Value: "W9"})
		rows(t, table, [][]any{{int64(0), nil, nil}})
		if table.Total != 1 {
			t.Errorf("total = %d; want 1", table.Total)
		}
	})

	t.Run("groups without measure", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "project_id->name"}}})
		rows(t, table, [][]any{{"djinn"}, {nil}})
	})

	t.Run("sorts name columns, limit keeps the total", func(t *testing.T) {
		t.Parallel()
		a := Aggregation{Location: time.UTC, By: []GroupBy{{Ref: "create_time", Granularity: Day}},
			Measures: []Measure{{Fn: Count}}}
		table, err := c.Run(t.Context(), Query{Collection: "TASKS", Aggregation: &a, Limit: 2,
			Sorts: []Sort{{Ref: "count", Desc: true}, {Ref: "create_time::day", Desc: true}}})
		if err != nil {
			t.Fatal(err)
		}
		rows(t, table, [][]any{{nov(2, 0), int64(2)}, {nov(4, 0), int64(1)}})
		if table.Total != 3 {
			t.Errorf("total = %d; want 3 groups", table.Total)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			q    Query
		}{
			{"empty", Query{Aggregation: &Aggregation{}}},
			{"selects", Query{Selects: []string{"code"}, Aggregation: &Aggregation{Measures: []Measure{{Fn: Count}}}}},
			{"granularity of a text", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "code", Granularity: Day}}}}},
			{"no such granularity", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "create_time", Granularity: 9}}}}},
			{"group by a list", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "depends_on"}}}}},
			{"group by nothing", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "nope"}}}}},
			{"group by hidden", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "prompt"}}}}},
			{"a group twice", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "code"}, {Ref: "code"}}}}},
			{"a name twice", Query{Aggregation: &Aggregation{By: []GroupBy{{Ref: "code"}},
				Measures: []Measure{{Fn: Count, Alias: "code"}}}}},
			{"no function", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "running"}}}}},
			{"sum of rows", Query{Aggregation: &Aggregation{Measures: []Measure{{Fn: Sum}}}}},
			{"sum of a text", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "code", Fn: Sum}}}}},
			{"avg of a time", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "create_time", Fn: Avg}}}}},
			{"max of an enum", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "status", Fn: Max}}}}},
			{"median of a bool", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "urgent", Fn: Median}}}}},
			{"min of a list", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "depends_on", Fn: Min}}}}},
			{"measure of nothing", Query{Aggregation: &Aggregation{Measures: []Measure{{Ref: "nope", Fn: Count}}}}},
			{"sort by a ref not a column", Query{Sorts: []Sort{{Ref: "code"}},
				Aggregation: &Aggregation{Measures: []Measure{{Fn: Count}}}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				tc.q.Collection = "TASKS"
				if _, err := c.Run(t.Context(), tc.q); !errors.Is(err, ErrInvalid) {
					t.Errorf("Run = %v; want ErrInvalid", err)
				}
			})
		}
	})

	t.Run("a count of a list counts the rows where it is not empty", func(t *testing.T) {
		t.Parallel()
		table := aggregate(t, c, Aggregation{Measures: []Measure{{Ref: "depends_on", Fn: Count}}})
		rows(t, table, [][]any{{int64(1)}})
	})
}

func TestCompute(t *testing.T) {
	t.Parallel()
	ints := []any{int64(4), int64(1), int64(3), int64(2)}
	durations := []any{4 * time.Second, time.Second, 3 * time.Second, 2 * time.Second}
	for _, tc := range []struct {
		name   string
		fn     Fn
		typ    Type
		values []any
		want   any
	}{
		{"median of an even count interpolates", Median, TypeInt, ints, 2.5},
		{"p90 interpolates", P90, TypeInt, ints, 3.7},
		{"median of one", Median, TypeFloat, []any{0.5}, 0.5},
		{"p90 of one", P90, TypeFloat, []any{0.5}, 0.5},
		{"median of an odd count", Median, TypeFloat, []any{3.0, 1.0, 2.0}, 2.0},
		{"avg of ints is a float", Avg, TypeInt, ints, 2.5},
		{"sum of ints", Sum, TypeInt, ints, int64(10)},
		{"min", Min, TypeInt, ints, int64(1)},
		{"max", Max, TypeInt, ints, int64(4)},
		{"median of durations", Median, TypeDuration, durations, 2500 * time.Millisecond},
		{"p90 of durations", P90, TypeDuration, durations, 3700 * time.Millisecond},
		{"avg of durations", Avg, TypeDuration, durations, 2500 * time.Millisecond},
		{"sum of durations", Sum, TypeDuration, durations, 10 * time.Second},
		{"max of durations", Max, TypeDuration, durations, 4 * time.Second},
		{"none is null", Median, TypeInt, nil, nil},
		{"none sums to null", Sum, TypeInt, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := compute(tc.fn, tc.typ, slices.Clone(tc.values))
			if f, ok := got.(float64); ok {
				if w, ok := tc.want.(float64); ok && f-w < 1e-9 && w-f < 1e-9 {
					return
				}
			}
			if got != tc.want {
				t.Errorf("%s = %#v; want %#v", tc.fn, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Paris leaves summer time on Sunday 25 October 2026, at 03:00.
	for _, tc := range []struct {
		in   string
		g    Granularity
		want string
	}{
		{"2026-10-25T12:00:00Z", Day, "2026-10-24T22:00:00Z"},
		{"2026-10-26T12:00:00Z", Day, "2026-10-25T23:00:00Z"},
		{"2026-10-25T22:30:00Z", Day, "2026-10-24T22:00:00Z"}, // 23:30 in Paris, still the 25th
		{"2026-10-25T12:00:00Z", Week, "2026-10-18T22:00:00Z"},
		{"2026-10-26T00:30:00Z", Week, "2026-10-25T23:00:00Z"},
		{"2026-11-01T00:00:00Z", Month, "2026-10-31T23:00:00Z"},
		{"2026-10-31T22:59:59Z", Month, "2026-09-30T22:00:00Z"},
	} {
		got := truncate(at(tc.in), tc.g, paris)
		if !got.Equal(at(tc.want)) || got.Location() != time.UTC {
			t.Errorf("truncate(%s, %s) = %s; want %s", tc.in, tc.g, got, tc.want)
		}
	}
}

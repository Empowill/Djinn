package query

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestParseFilter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want Filter
	}{
		{"status=running", Equal{Ref: "status", Value: "running"}},
		{"wish_id->title=Stats", Equal{Ref: "wish_id->title", Value: "Stats"}},
		{"status=done|failed", In{Ref: "status", Values: []any{"done", "failed"}}},
		{"status=null", IsNull{Ref: "status"}},
		{"status=pending|null", In{Ref: "status", Values: []any{"pending"}, IncludeNull: true}},
		{"status!=done", Group{Op: Not, Filters: []Filter{Equal{Ref: "status", Value: "done"}}}},
		{"status!=null", Group{Op: Not, Filters: []Filter{IsNull{Ref: "status"}}}},
		{"usage.cost_usd>1", Range{Ref: "usage.cost_usd", Gt: "1"}},
		{"create_time>=2026-11-01", Range{Ref: "create_time", Gte: "2026-11-01"}},
		{"running<90s", Range{Ref: "running", Lt: "90s"}},
		{"prompt::size<=-1", Range{Ref: "prompt::size", Lte: "-1"}},
		{"wish_id->title>=S", Range{Ref: "wish_id->title", Gte: "S"}},
		{"title*=a=b|c", Text{Ref: "title", Contains: "a=b|c"}},
		{"code^=W", Text{Ref: "code", BeginsWith: "W"}},
	} {
		got, err := ParseFilter(tt.in)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseFilter(%q) = %#v, %v; want %#v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "=x", "status", "status running", "status=", "status~x"} {
		if _, err := ParseFilter(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseFilter(%q) = %v; want ErrInvalid", in, err)
		}
	}
}

func TestParseSortGroupMeasure(t *testing.T) {
	t.Parallel()
	if s, err := ParseSort("-create_time"); err != nil || s != (Sort{Ref: "create_time", Desc: true}) {
		t.Errorf("ParseSort(-create_time) = %v, %v", s, err)
	}
	if s, err := ParseSort("code"); err != nil || s != (Sort{Ref: "code"}) {
		t.Errorf("ParseSort(code) = %v, %v", s, err)
	}
	if _, err := ParseSort("-"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ParseSort(-) = %v; want ErrInvalid", err)
	}

	for _, tt := range []struct {
		in   string
		want GroupBy
	}{
		{"status", GroupBy{Ref: "status"}},
		{"create_time::week", GroupBy{Ref: "create_time", Granularity: Week}},
		{"wait_question_id->create_time::month", GroupBy{Ref: "wait_question_id->create_time", Granularity: Month}},
		{"running::bins(0s, 10s,1m)", GroupBy{Ref: "running", Bounds: []any{"0s", "10s", "1m"}}},
		{"answer::delay::bins(0s,1h)", GroupBy{Ref: "answer::delay", Bounds: []any{"0s", "1h"}}},
		{"answer::delay", GroupBy{Ref: "answer::delay"}},
	} {
		got, err := ParseGroup(tt.in)
		if err != nil || got.Ref != tt.want.Ref || got.Granularity != tt.want.Granularity ||
			!slices.Equal(got.Bounds, tt.want.Bounds) {
			t.Errorf("ParseGroup(%q) = %#v, %v; want %#v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "running::bins(1s,2s", "running::bins(1s,,2s)", "::bins(1s,2s)"} {
		if _, err := ParseGroup(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseGroup(%q) = %v; want ErrInvalid", in, err)
		}
	}

	for _, tt := range []struct {
		in   string
		want Measure
	}{
		{"count", Measure{Fn: Count}},
		{"n=count", Measure{Fn: Count, Alias: "n"}},
		{"running::median", Measure{Ref: "running", Fn: Median}},
		{"answer::delay::p90", Measure{Ref: "answer::delay", Fn: P90}},
		{"cost = usage.cost_usd::sum", Measure{Ref: "usage.cost_usd", Fn: Sum, Alias: "cost"}},
		{"wish_id::count", Measure{Ref: "wish_id", Fn: Count}},
	} {
		if got, err := ParseMeasure(tt.in); err != nil || got != tt.want {
			t.Errorf("ParseMeasure(%q) = %#v, %v; want %#v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "running", "running::mean", "::sum", "=count"} {
		if _, err := ParseMeasure(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseMeasure(%q) = %v; want ErrInvalid", in, err)
		}
	}
}

// TestParsedFilters runs the filters' texts on the frozen set: what each keeps, and the equality the store narrows by.
func TestParsedFilters(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		filter string
		want   []string
	}{
		{"status=running", []string{"W2"}},
		{"status=RUNNING|failed", []string{"W2", "W3"}},
		{"status=null", []string{"W1"}},
		{"status=done|null", []string{"W1", "W1"}},
		{"status!=done", []string{"W2", "W1", "W3"}},
		{"status!=null", []string{"W1", "W2", "W3"}},
		{"usage.cost_usd>0", []string{"W1", "W3"}},
		{"usage.cost_usd<=0.25", []string{"W2", "W3"}},
		{"create_time>=2026-11-03T09:00:00Z", []string{"W2", "W1"}},
		{"running>=1h", []string{"W1"}},
		{"wish_id->title=stats", []string{"W1", "W2"}},
		{"title*=QUERY", []string{"W1"}},
		{"title^=a", []string{"W2", "W3"}},
		{"code=w1", []string{"W1", "W1"}},
	} {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()
			c, _ := catalog(t)
			f, err := ParseFilter(tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			table, err := c.Run(t.Context(), Query{Collection: "TASKS", Selects: []string{"code"}, Filters: []Filter{f}})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, row := range table.Rows {
				got = append(got, row[0].(string))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("codes = %v; want %v", got, tt.want)
			}
		})
	}

	t.Run("an equality on an indexed field narrows the load", func(t *testing.T) {
		t.Parallel()
		c, tasks := catalog(t)
		f, err := ParseFilter("wish_id=" + id(3))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Run(t.Context(), Query{Collection: "TASKS", Filters: []Filter{f}}); err != nil {
			t.Fatal(err)
		}
		if len(tasks.wheres) != 1 || tasks.wheres[0]["wish_id"] != id(3) {
			t.Errorf("wheres = %v; want wish_id narrowed", tasks.wheres)
		}
	})
}

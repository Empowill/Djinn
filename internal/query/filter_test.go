package query

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestFilter(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	for _, tc := range []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"equal enum short name", Equal{Ref: "status", Value: "done"}, []string{"W1"}},
		{"equal text ignores case", Equal{Ref: "title", Value: "CORE QUERY"}, []string{"W1"}},
		{"equal 0 is not null", Equal{Ref: "usage.cost_usd", Value: 0}, []string{"W2"}},
		{"equal false", Equal{Ref: "urgent", Value: false}, []string{"W1", "W2", "W3"}},
		{"equal on a list", Equal{Ref: "depends_on", Value: id(4)}, []string{"W2"}},
		{"equal across a jump", Equal{Ref: "wish_id->state", Value: "GRANTED"}, []string{"W1"}},
		{"in", In{Ref: "status", Values: []any{"DONE", "FAILED"}}, []string{"W1", "W3"}},
		{"in with null", In{Ref: "status", Values: []any{"FAILED"}, IncludeNull: true}, []string{"W1", "W3"}},
		{"in null only", In{Ref: "usage.cost_usd", IncludeNull: true}, []string{"W1"}},
		{"range float", Range{Ref: "usage.cost_usd", Gt: 0, Lte: "1.5"}, []string{"W1", "W3"}},
		{"range open", Range{Ref: "usage.cost_usd", Gte: 0, Lt: 1}, []string{"W2", "W3"}},
		{"range int", Range{Ref: "usage.input_tokens", Gte: 1}, []string{"W1"}},
		{"range time", Range{Ref: "create_time", Gte: day0.Add(time.Hour), Lt: "2026-11-04T09:00:00Z"},
			[]string{"W2", "W3"}},
		{"range duration", Range{Ref: "running", Gt: "1h"}, []string{"W1"}},
		{"text contains", Text{Ref: "title", Contains: "AG"}, []string{"W2", "W1"}},
		{"text begins", Text{Ref: "title", BeginsWith: "a"}, []string{"W2", "W3"}},
		{"text on an enum", Text{Ref: "status", Contains: "fail"}, []string{"W3"}},
		{"text on a list", Text{Ref: "depends_on", BeginsWith: "0190"}, []string{"W2"}},
		{"is null", IsNull{Ref: "end_time"}, []string{"W2", "W1", "W3"}},
		{"is null across a broken jump", IsNull{Ref: "wish_id->title"}, []string{"W3"}},
		{"not null", Group{Op: Not, Filters: []Filter{IsNull{Ref: "usage.cost_usd"}}}, []string{"W1", "W2", "W3"}},
		{"or", Group{Op: Or, Filters: []Filter{Equal{Ref: "code", Value: "W3"}, Equal{Ref: "urgent", Value: true}}},
			[]string{"W1", "W3"}},
		{"and", Group{Op: And, Filters: []Filter{Equal{Ref: "code", Value: "W1"},
			Equal{Ref: "wish_id->title", Value: "Stats"}}}, []string{"W1"}},
		{"nested", Group{Op: Not, Filters: []Filter{Group{Op: Or, Filters: []Filter{
			Equal{Ref: "status", Value: "RUNNING"}, IsNull{Ref: "status"}}}}}, []string{"W1", "W3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := codes(t, c, Query{Collection: "TASKS", Filters: []Filter{tc.filter}})
			if !slices.Equal(got, tc.want) {
				t.Errorf("codes = %v; want %v", got, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		filter Filter
	}{
		{"empty group", Group{Op: And}},
		{"group without op", Group{Filters: []Filter{IsNull{Ref: "code"}}}},
		{"nil in a group", Group{Op: Or, Filters: []Filter{nil}}},
		{"bad ref in a group", Group{Op: Or, Filters: []Filter{IsNull{Ref: "nope"}}}},
		{"equal null", Equal{Ref: "code", Value: nil}},
		{"equal bad ref", Equal{Ref: "nope", Value: "x"}},
		{"in nothing", In{Ref: "code"}},
		{"in bad value", In{Ref: "status", Values: []any{"LOST"}}},
		{"in bad ref", In{Ref: "nope", Values: []any{"x"}}},
		{"range on text", Range{Ref: "code", Gt: "W1"}},
		{"range without bound", Range{Ref: "running"}},
		{"range bad bound", Range{Ref: "running", Gt: "soon"}},
		{"range bad ref", Range{Ref: "nope", Gt: 1}},
		{"text on a number", Text{Ref: "usage.cost_usd", Contains: "1"}},
		{"text both", Text{Ref: "code", Contains: "W", BeginsWith: "W"}},
		{"text neither", Text{Ref: "code"}},
		{"text bad ref", Text{Ref: "nope", Contains: "x"}},
		{"is null hidden", IsNull{Ref: "prompt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.Run(t.Context(), Query{Collection: "TASKS", Filters: []Filter{tc.filter}})
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Run = %v; want ErrInvalid", err)
			}
		})
	}
}

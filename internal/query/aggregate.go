package query

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Aggregation turns a query's rows into groups: a row per group of rows whose By refs hold the same values, with a
// column per group ref, then a column per measure. Without By, the whole set is one group, and the table one row, even
// when no row holds: its count is 0, its other measures null. Otherwise a group holds one row at least: an empty
// period never shows. Rows come ordered by their groups' values, nulls last, before the query's sorts, which name the
// table's columns.
type Aggregation struct {
	By       []GroupBy
	Measures []Measure
	// Location is where a day, a week or a month starts: the machine's local time when nil.
	Location *time.Location
}

// GroupBy groups rows by a ref's value, which cannot be a list. Text groups as written, case included; null is a group
// of its own.
type GroupBy struct {
	Ref string
	// Granularity truncates a time to the start of its period: the column is then named Ref::day, ::week or ::month,
	// and holds that start, in UTC.
	Granularity Granularity
}

// Granularity is the period a group truncates a time to.
type Granularity int

// The granularities of a group.
const (
	// Day starts at midnight.
	Day Granularity = iota + 1
	// Week starts on Monday at midnight.
	Week
	// Month starts on its first day at midnight.
	Month
)

func (g Granularity) String() string {
	switch g {
	case Day:
		return "day"
	case Week:
		return "week"
	case Month:
		return "month"
	}
	return ""
}

// Measure computes a function over the values of a ref in each group. Every function skips the nulls: the count
// counts the values, the others are null for a group without value. Its column is named Alias, or Ref::fn
// (running::median), or count for a count of rows.
type Measure struct {
	// Ref is the ref read: empty for a count of rows.
	Ref   string
	Fn    Fn
	Alias string
}

// Fn is a measure's function.
type Fn int

// The functions of a measure, and the types they take. A list only counts, as a row whose list is not empty.
const (
	// Count counts the rows, or the rows where Ref is not null: an int, 0 for none.
	Count Fn = iota + 1
	// Sum adds ints, floats or durations, of the type it reads.
	Sum
	// Avg is the mean of ints or floats as a float, of durations as a duration.
	Avg
	// Min and Max take ints, floats, durations or times, of the type they read.
	Min
	Max
	// Median and P90 interpolate between the two closest values (as Postgres's percentile_cont does): a float for
	// ints and floats, a duration for durations.
	Median
	P90
)

func (f Fn) String() string {
	switch f {
	case Count:
		return "count"
	case Sum:
		return "sum"
	case Avg:
		return "avg"
	case Min:
		return "min"
	case Max:
		return "max"
	case Median:
		return "median"
	case P90:
		return "p90"
	}
	return ""
}

// aggregator is an aggregation compiled: its group refs, then its measures, in the table's order.
type aggregator struct {
	loc      *time.Location
	groups   []compiled
	measures []measure
	columns  []Column
}

type measure struct {
	fn  Fn
	ref *compiled // nil for a count of rows
	typ Type
}

// aggregation compiles a's refs of md.
func (c *compiler) aggregation(md protoreflect.MessageDescriptor, a Aggregation) (*aggregator, error) {
	if len(a.By) == 0 && len(a.Measures) == 0 {
		return nil, fmt.Errorf("%w: an aggregation without group nor measure", ErrInvalid)
	}
	ag := &aggregator{loc: a.Location}
	if ag.loc == nil {
		ag.loc = time.Local
	}
	named := func(name string) error {
		if slices.ContainsFunc(ag.columns, func(c Column) bool { return c.Ref == name }) {
			return fmt.Errorf("%w: column %q twice", ErrInvalid, name)
		}
		return nil
	}
	for _, g := range a.By {
		r, err := c.ref(md, g.Ref)
		if err != nil {
			return nil, err
		}
		if r.col.List {
			return nil, fmt.Errorf("%w: ref %q: a list does not group", ErrInvalid, g.Ref)
		}
		col := r.col
		col.Of = g.Ref
		if g.Granularity != 0 {
			if g.Granularity.String() == "" {
				return nil, fmt.Errorf("%w: ref %q: no granularity %d", ErrInvalid, g.Ref, g.Granularity)
			}
			if col.Type != TypeTime {
				return nil, fmt.Errorf("%w: ref %q: a %s has no %s", ErrInvalid, g.Ref, col.Type, g.Granularity)
			}
			col.Ref = g.Ref + "::" + g.Granularity.String()
			col.Granularity = g.Granularity
			get, loc, gran := r.get, ag.loc, g.Granularity
			r.get = func(m protoreflect.Message) any {
				t, ok := get(m).(time.Time)
				if !ok {
					return nil
				}
				return truncate(t, gran, loc)
			}
		}
		if err := named(col.Ref); err != nil {
			return nil, err
		}
		r.col = col
		ag.groups = append(ag.groups, r)
		ag.columns = append(ag.columns, col)
	}
	for _, ms := range a.Measures {
		name := ms.Fn.String()
		if name == "" {
			return nil, fmt.Errorf("%w: measure of %q: no function %d", ErrInvalid, ms.Ref, ms.Fn)
		}
		m := measure{fn: ms.Fn}
		col := Column{Fn: ms.Fn, Of: ms.Ref}
		if ms.Ref == "" {
			if ms.Fn != Count {
				return nil, fmt.Errorf("%w: a %s needs a ref", ErrInvalid, name)
			}
		} else {
			r, err := c.ref(md, ms.Ref)
			if err != nil {
				return nil, err
			}
			m.ref = &r
			col.Field, col.Doc = r.col.Field, r.col.Doc
			name = ms.Ref + "::" + name
			if ms.Fn != Count && r.col.List {
				return nil, fmt.Errorf("%w: ref %q: a list only counts", ErrInvalid, ms.Ref)
			}
			if m.typ = measured(ms.Fn, r.col.Type); m.typ == 0 {
				return nil, fmt.Errorf("%w: ref %q: no %s of a %s", ErrInvalid, ms.Ref, ms.Fn, r.col.Type)
			}
		}
		if ms.Fn == Count {
			m.typ = TypeInt
		}
		if ms.Alias != "" {
			name = ms.Alias
		}
		if err := named(name); err != nil {
			return nil, err
		}
		col.Ref, col.Type = name, m.typ
		ag.measures = append(ag.measures, m)
		ag.columns = append(ag.columns, col)
	}
	return ag, nil
}

// measured is the type fn gives over values of typ: 0 when it takes none.
func measured(fn Fn, typ Type) Type {
	switch fn {
	case Count:
		return TypeInt
	case Sum:
		switch typ {
		case TypeInt, TypeFloat, TypeDuration:
			return typ
		}
	case Avg, Median, P90:
		switch typ {
		case TypeInt, TypeFloat:
			return TypeFloat
		case TypeDuration:
			return TypeDuration
		}
	case Min, Max:
		switch typ {
		case TypeInt, TypeFloat, TypeDuration, TypeTime:
			return typ
		}
	}
	return 0
}

// truncate gives the start of t's period in loc, in UTC.
func truncate(t time.Time, g Granularity, loc *time.Location) time.Time {
	t = t.In(loc)
	y, m, d := t.Date()
	switch g {
	case Week:
		d -= (int(t.Weekday()) + 6) % 7
	case Month:
		d = 1
	}
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC()
}

// run groups rows and computes the measures: a row per group, in the order of its values.
func (ag *aggregator) run(rows []protoreflect.Message) [][]any {
	type group struct {
		keys   []any
		values [][]any // per measure, its values not null
		count  []int64 // per measure, a count's
	}
	var groups []*group
	byKey := map[string]*group{}
	if len(ag.groups) == 0 {
		g := &group{values: make([][]any, len(ag.measures)), count: make([]int64, len(ag.measures))}
		groups, byKey[""] = append(groups, g), g
	}
	for _, m := range rows {
		keys := make([]any, len(ag.groups))
		for i, r := range ag.groups {
			keys[i] = r.get(m)
		}
		k := key(keys)
		g, ok := byKey[k]
		if !ok {
			g = &group{keys: keys, values: make([][]any, len(ag.measures)), count: make([]int64, len(ag.measures))}
			byKey[k] = g
			groups = append(groups, g)
		}
		for i, ms := range ag.measures {
			if ms.ref == nil {
				g.count[i]++
				continue
			}
			v := ms.ref.get(m)
			if v == nil {
				continue
			}
			if ms.fn == Count {
				g.count[i]++
				continue
			}
			g.values[i] = append(g.values[i], v)
		}
	}
	slices.SortStableFunc(groups, func(a, b *group) int {
		return order(ag.columns, nil, a.keys, b.keys)
	})
	out := make([][]any, 0, len(groups))
	for _, g := range groups {
		row := slices.Clone(g.keys)
		for i, ms := range ag.measures {
			if ms.fn == Count {
				row = append(row, g.count[i])
				continue
			}
			row = append(row, compute(ms.fn, ms.ref.col.Type, g.values[i]))
		}
		out = append(out, row)
	}
	return out
}

// key is a map key for a group's values: Go syntax quotes text, and a time reads as its instant.
func key(values []any) string {
	var b strings.Builder
	for _, v := range values {
		if t, ok := v.(time.Time); ok {
			v = t.UnixNano()
		}
		fmt.Fprintf(&b, "%#v,", v)
	}
	return b.String()
}

// compute applies fn to values of typ, none of them null: nil for none.
func compute(fn Fn, typ Type, values []any) any {
	if len(values) == 0 {
		return nil
	}
	switch fn {
	case Min, Max:
		best := values[0]
		for _, v := range values[1:] {
			c := compare(Column{Type: typ}, v, best)
			if (fn == Min && c < 0) || (fn == Max && c > 0) {
				best = v
			}
		}
		return best
	case Sum:
		switch typ {
		case TypeInt:
			var s int64
			for _, v := range values {
				s += v.(int64)
			}
			return s
		case TypeDuration:
			var s time.Duration
			for _, v := range values {
				s += v.(time.Duration)
			}
			return s
		}
		var s float64
		for _, v := range values {
			s += v.(float64)
		}
		return s
	}
	xs := make([]float64, 0, len(values))
	for _, v := range values {
		switch n := v.(type) {
		case int64:
			xs = append(xs, float64(n))
		case float64:
			xs = append(xs, n)
		case time.Duration:
			xs = append(xs, float64(n))
		}
	}
	var r float64
	switch fn {
	case Avg:
		for _, x := range xs {
			r += x
		}
		r /= float64(len(xs))
	case Median:
		r = percentile(xs, 0.5)
	case P90:
		r = percentile(xs, 0.9)
	}
	if typ == TypeDuration {
		return time.Duration(math.Round(r))
	}
	return r
}

// percentile interpolates the p-th fraction of xs between its two closest values, once sorted.
func percentile(xs []float64, p float64) float64 {
	slices.Sort(xs)
	h := float64(len(xs)-1) * p
	lo := int(math.Floor(h))
	if lo+1 >= len(xs) {
		return xs[lo]
	}
	return xs[lo] + (h-float64(lo))*(xs[lo+1]-xs[lo])
}

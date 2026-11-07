package query

import (
	"fmt"
	"strings"
)

// The texts a request writes a query in, one filter, sort, group or measure each: what the command line types, and
// what QueryService reads.

// null is the value that stands for an absent one in a filter's text.
const null = "null"

// ops are a filter's operators, the longer first where one starts another.
var ops = []string{"!=", ">=", "<=", "*=", "^=", "=", ">", "<"}

// ParseFilter reads a filter written ref, an operator, a value:
//
//	ref=v      Equal; ref=a|b In, ref=null IsNull, ref=a|null In with IncludeNull
//	ref!=v     Not of the same: ref!=a|b holds for none of them, an absent value included; ref!=null for a value
//	ref>v      Range, and >=, <, <=
//	ref*=v     Text that contains v; ref^=v Text that begins with v
//
// The value is the rest of the text, as written: | separates the values of = and != only.
func ParseFilter(s string) (Filter, error) {
	ref, rest := splitRef(s)
	if ref == "" {
		return nil, fmt.Errorf("%w: filter %q: no ref first", ErrInvalid, s)
	}
	op := ""
	for _, o := range ops {
		if strings.HasPrefix(rest, o) {
			op = o
			break
		}
	}
	if op == "" {
		return nil, fmt.Errorf("%w: filter %q: no operator after %s: = != > >= < <= *= ^=", ErrInvalid, s, ref)
	}
	value := rest[len(op):]
	if value == "" {
		return nil, fmt.Errorf("%w: filter %q: no value; null is the absent one", ErrInvalid, s)
	}
	switch op {
	case "=", "!=":
		f := oneOf(ref, strings.Split(value, "|"))
		if op == "!=" {
			f = Group{Op: Not, Filters: []Filter{f}}
		}
		return f, nil
	case ">":
		return Range{Ref: ref, Gt: value}, nil
	case ">=":
		return Range{Ref: ref, Gte: value}, nil
	case "<":
		return Range{Ref: ref, Lt: value}, nil
	case "<=":
		return Range{Ref: ref, Lte: value}, nil
	case "*=":
		return Text{Ref: ref, Contains: value}, nil
	}
	return Text{Ref: ref, BeginsWith: value}, nil
}

// oneOf is the filter that holds when ref's value is one of values, null among them for an absent one.
func oneOf(ref string, values []string) Filter {
	in := In{Ref: ref}
	for _, v := range values {
		if v == null {
			in.IncludeNull = true
			continue
		}
		in.Values = append(in.Values, v)
	}
	switch {
	case len(in.Values) == 0:
		return IsNull{Ref: ref}
	case len(in.Values) == 1 && !in.IncludeNull:
		// An Equal, which the source narrows its rows by when the field is indexed.
		return Equal{Ref: ref, Value: in.Values[0]}
	}
	return in
}

// splitRef cuts the ref at the head of s: names of letters, digits and _, joined by ., :: or ->.
func splitRef(s string) (ref, rest string) {
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.', c == ':':
			i++
		case strings.HasPrefix(s[i:], "->"):
			i += 2
		default:
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// ParseSort reads a sort: ref ascending, -ref descending.
func ParseSort(s string) (Sort, error) {
	ref, desc := strings.CutPrefix(strings.TrimSpace(s), "-")
	if ref == "" {
		return Sort{}, fmt.Errorf("%w: sort %q: no ref", ErrInvalid, s)
	}
	return Sort{Ref: ref, Desc: desc}, nil
}

// ParseGroup reads a group: ref, ref::day, ref::week, ref::month, or ref::bins(b1,b2,...).
func ParseGroup(s string) (GroupBy, error) {
	s = strings.TrimSpace(s)
	if head, bounds, ok := strings.Cut(s, "::bins("); ok {
		inner, closed := strings.CutSuffix(bounds, ")")
		if !closed || head == "" {
			return GroupBy{}, fmt.Errorf("%w: group %q: write ref::bins(b1,b2,...)", ErrInvalid, s)
		}
		g := GroupBy{Ref: head}
		for b := range strings.SplitSeq(inner, ",") {
			if b = strings.TrimSpace(b); b == "" {
				return GroupBy{}, fmt.Errorf("%w: group %q: an empty bound", ErrInvalid, s)
			}
			g.Bounds = append(g.Bounds, b)
		}
		return g, nil
	}
	for _, gran := range []Granularity{Day, Week, Month} {
		if head, ok := strings.CutSuffix(s, "::"+gran.String()); ok && head != "" {
			return GroupBy{Ref: head, Granularity: gran}, nil
		}
	}
	if s == "" {
		return GroupBy{}, fmt.Errorf("%w: an empty group", ErrInvalid)
	}
	return GroupBy{Ref: s}, nil
}

// ParseMeasure reads a measure: count, ref::fn, or name=ref::fn (name=count) to name its column.
func ParseMeasure(s string) (Measure, error) {
	var m Measure
	body := strings.TrimSpace(s)
	if alias, rest, ok := strings.Cut(body, "="); ok {
		m.Alias, body = strings.TrimSpace(alias), strings.TrimSpace(rest)
		if m.Alias == "" {
			return Measure{}, fmt.Errorf("%w: measure %q: an empty name", ErrInvalid, s)
		}
	}
	if body == Count.String() {
		m.Fn = Count
		return m, nil
	}
	i := strings.LastIndex(body, "::")
	if i <= 0 {
		return Measure{}, fmt.Errorf("%w: measure %q: write count, or ref::fn", ErrInvalid, s)
	}
	m.Ref = body[:i]
	name := body[i+2:]
	for fn := Count; fn <= P90; fn++ {
		if fn.String() == name {
			m.Fn = fn
			return m, nil
		}
	}
	return Measure{}, fmt.Errorf("%w: measure %q: no function %s: count sum avg min max median p90", ErrInvalid, s, name)
}

package query

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Filter keeps the rows it holds for. A null value holds for none but IsNull, and In with IncludeNull. On a list,
// a filter holds when it holds for one of the list's values.
type Filter interface {
	compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error)
}

type pred func(m protoreflect.Message) bool

// Op is how a Group joins its filters.
type Op int

// The ops of a Group.
const (
	// And holds when every filter holds.
	And Op = iota + 1
	// Or holds when one filter holds.
	Or
	// Not holds when the filters, joined as And, do not.
	Not
)

// Group joins filters: one at least.
type Group struct {
	Op      Op
	Filters []Filter
}

// Equal holds when Ref's value is Value: text compared ignoring case, as the store compares it.
type Equal struct {
	Ref   string
	Value any
}

// In holds when Ref's value is one of Values, or null with IncludeNull.
type In struct {
	Ref         string
	Values      []any
	IncludeNull bool
}

// Range holds when Ref's value, a number, a time or a duration, is within each bound given.
type Range struct {
	Ref              string
	Gt, Gte, Lt, Lte any
}

// Text holds when Ref's text contains Contains, or begins with BeginsWith, ignoring case: one of the two.
type Text struct {
	Ref        string
	Contains   string
	BeginsWith string
}

// IsNull holds when Ref's value is absent.
type IsNull struct {
	Ref string
}

// typed is a filter a typed comparison of QueryService gives: its ref must be of one of types.
type typed struct {
	Filter
	ref   string
	kind  string
	types []Type
}

// bind is f, which may only read ref of one of types: a kind filter of QueryService.
func bind(ref, kind string, f Filter, types ...Type) Filter {
	return typed{Filter: f, ref: ref, kind: kind, types: types}
}

func (f typed) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.ref)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(f.types, r.col.Type) {
		return nil, fmt.Errorf("%w: a %s filter on %s, a %s", ErrInvalid, f.kind, f.ref, r.col.Type)
	}
	return f.Filter.compile(c, md)
}

func (g Group) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	if len(g.Filters) == 0 {
		return nil, fmt.Errorf("%w: an empty group", ErrInvalid)
	}
	preds := make([]pred, 0, len(g.Filters))
	for _, f := range g.Filters {
		if f == nil {
			return nil, fmt.Errorf("%w: a nil filter", ErrInvalid)
		}
		p, err := f.compile(c, md)
		if err != nil {
			return nil, err
		}
		preds = append(preds, p)
	}
	all := func(m protoreflect.Message) bool {
		for _, p := range preds {
			if !p(m) {
				return false
			}
		}
		return true
	}
	switch g.Op {
	case And:
		return all, nil
	case Not:
		return func(m protoreflect.Message) bool { return !all(m) }, nil
	case Or:
		return func(m protoreflect.Message) bool {
			for _, p := range preds {
				if p(m) {
					return true
				}
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("%w: a group without op", ErrInvalid)
}

func (f Equal) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.Ref)
	if err != nil {
		return nil, err
	}
	want, err := coerce(r.col, f.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return each(r, func(v any) bool { return equal(r.col, v, want) }), nil
}

func (f In) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.Ref)
	if err != nil {
		return nil, err
	}
	if len(f.Values) == 0 && !f.IncludeNull {
		return nil, fmt.Errorf("%w: %s: in no value", ErrInvalid, f.Ref)
	}
	wants := make([]any, 0, len(f.Values))
	for _, v := range f.Values {
		w, err := coerce(r.col, v)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		wants = append(wants, w)
	}
	some := each(r, func(v any) bool {
		for _, w := range wants {
			if equal(r.col, v, w) {
				return true
			}
		}
		return false
	})
	return func(m protoreflect.Message) bool {
		if f.IncludeNull && r.get(m) == nil {
			return true
		}
		return some(m)
	}, nil
}

func (f Range) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.Ref)
	if err != nil {
		return nil, err
	}
	switch r.col.Type {
	case TypeInt, TypeFloat, TypeTime, TypeDuration:
	default:
		return nil, fmt.Errorf("%w: %s: a range on a %s", ErrInvalid, f.Ref, r.col.Type)
	}
	type bound struct {
		v  any
		ok func(c int) bool
	}
	var bounds []bound
	for _, b := range []bound{
		{f.Gt, func(c int) bool { return c > 0 }},
		{f.Gte, func(c int) bool { return c >= 0 }},
		{f.Lt, func(c int) bool { return c < 0 }},
		{f.Lte, func(c int) bool { return c <= 0 }},
	} {
		if b.v == nil {
			continue
		}
		v, err := coerce(r.col, b.v)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		bounds = append(bounds, bound{v, b.ok})
	}
	if len(bounds) == 0 {
		return nil, fmt.Errorf("%w: %s: a range without bound", ErrInvalid, f.Ref)
	}
	return each(r, func(v any) bool {
		for _, b := range bounds {
			if !b.ok(compare(r.col, v, b.v)) {
				return false
			}
		}
		return true
	}), nil
}

func (f Text) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.Ref)
	if err != nil {
		return nil, err
	}
	if r.col.Type != TypeString && r.col.Type != TypeEnum {
		return nil, fmt.Errorf("%w: %s: a text filter on a %s", ErrInvalid, f.Ref, r.col.Type)
	}
	if (f.Contains == "") == (f.BeginsWith == "") {
		return nil, fmt.Errorf("%w: %s: a text filter needs one of contains and begins with", ErrInvalid, f.Ref)
	}
	contains, begins := strings.ToLower(f.Contains), strings.ToLower(f.BeginsWith)
	return each(r, func(v any) bool {
		s := strings.ToLower(v.(string))
		if begins != "" {
			return strings.HasPrefix(s, begins)
		}
		return strings.Contains(s, contains)
	}), nil
}

func (f IsNull) compile(c *compiler, md protoreflect.MessageDescriptor) (pred, error) {
	r, err := c.ref(md, f.Ref)
	if err != nil {
		return nil, err
	}
	return func(m protoreflect.Message) bool { return r.get(m) == nil }, nil
}

// each holds when ok holds for r's value, or for one of its values on a list: never on null.
func each(r compiled, ok func(any) bool) pred {
	return func(m protoreflect.Message) bool {
		v := r.get(m)
		if v == nil {
			return false
		}
		if l, isList := v.([]any); isList {
			for _, x := range l {
				if ok(x) {
					return true
				}
			}
			return false
		}
		return ok(v)
	}
}

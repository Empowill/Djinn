package query

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// getter reads a ref's value in a message: nil when absent.
type getter func(m protoreflect.Message) any

// compiled is a ref ready to read.
type compiled struct {
	col Column
	get getter
}

// compiler compiles the refs of one query, and keeps the targets their jumps read, loaded before the rows.
type compiler struct {
	cat *Catalog
	// targets maps each message a jump reads to its rows by lowered id, filled once every ref is compiled.
	targets map[protoreflect.FullName]map[string]protoreflect.Message
}

// ref compiles a ref of md: a path of fields joined by dots, then, past a field whose (djinn.v1.ref) option names
// another collection, -> and a ref of that collection.
func (c *compiler) ref(md protoreflect.MessageDescriptor, ref string) (compiled, error) {
	head, rest, crosses := strings.Cut(ref, "->")
	if d, ok := c.cat.derived[md.FullName()][head]; ok && !crosses {
		get := d.get
		return compiled{col: d.column(), get: func(m protoreflect.Message) any { return get(m.Interface()) }}, nil
	}
	col, get, err := path(md, head, crosses)
	if err != nil {
		return compiled{}, fmt.Errorf("%w: ref %q: %w", ErrInvalid, ref, err)
	}
	if !crosses {
		return compiled{col: col, get: get}, nil
	}
	src, ok := c.cat.byType[col.Jump]
	if !ok {
		return compiled{}, fmt.Errorf("%w: ref %q: no collection holds %s", ErrInvalid, ref, col.Jump)
	}
	next, err := c.ref(src.Descriptor(), rest)
	if err != nil {
		return compiled{}, err
	}
	if _, ok := c.targets[col.Jump]; !ok {
		c.targets[col.Jump] = nil
	}
	target := col.Jump
	next.col.Ref = ref
	idGet, nextGet := get, next.get
	next.get = func(m protoreflect.Message) any {
		id, _ := idGet(m).(string)
		if id == "" {
			return nil
		}
		t, ok := c.targets[target][strings.ToLower(id)]
		if !ok {
			return nil
		}
		return nextGet(t)
	}
	return next, nil
}

// path compiles a path of fields in md. Ahead of a jump, it must end on a field with a (djinn.v1.ref) option.
func path(md protoreflect.MessageDescriptor, p string, ahead bool) (Column, getter, error) {
	if p == "" {
		return Column{}, nil, fmt.Errorf("an empty field name")
	}
	names := strings.Split(p, ".")
	var steps []protoreflect.FieldDescriptor
	for i, name := range names {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			return Column{}, nil, fmt.Errorf("%s has no field %s", md.FullName(), name)
		}
		if hidden(fd) {
			return Column{}, nil, fmt.Errorf("%s.%s is hidden", md.FullName(), name)
		}
		if fd.IsMap() {
			return Column{}, nil, fmt.Errorf("%s.%s is a map", md.FullName(), name)
		}
		if i == len(names)-1 {
			typ, ok := leaf(fd)
			if !ok {
				return Column{}, nil, fmt.Errorf("%s.%s is a message: name one of its fields", md.FullName(), name)
			}
			col := Column{Ref: p, Type: typ, List: fd.IsList(), Field: fd, Jump: jump(fd), Unit: unit(fd)}
			if ahead && col.Jump == "" {
				return Column{}, nil, fmt.Errorf("%s.%s names no other collection", md.FullName(), name)
			}
			return col, reader(steps, fd, typ), nil
		}
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() {
			return Column{}, nil, fmt.Errorf("%s.%s holds no fields", md.FullName(), name)
		}
		if _, ok := leaf(fd); ok {
			return Column{}, nil, fmt.Errorf("%s.%s holds no fields", md.FullName(), name)
		}
		steps = append(steps, fd)
		md = fd.Message()
	}
	panic("unreachable")
}

// reader reads fd in the message steps lead to: nil when one of them is absent.
func reader(steps []protoreflect.FieldDescriptor, fd protoreflect.FieldDescriptor, typ Type) getter {
	return func(m protoreflect.Message) any {
		for _, s := range steps {
			if !m.Has(s) {
				return nil
			}
			m = m.Get(s).Message()
		}
		return value(m, fd, typ)
	}
}

// value reads fd in m. Absent is nil, never a zero: a message or an optional field not set, an empty text, an enum
// left unspecified, an empty list. A number or a bool without presence is a value, 0 or false included, when the
// message that holds it is there.
func value(m protoreflect.Message, fd protoreflect.FieldDescriptor, typ Type) any {
	if fd.IsList() {
		l := m.Get(fd).List()
		if l.Len() == 0 {
			return nil
		}
		out := make([]any, 0, l.Len())
		for i := range l.Len() {
			if v := scalar(fd, typ, l.Get(i)); v != nil {
				out = append(out, v)
			}
		}
		return out
	}
	if fd.HasPresence() && !m.Has(fd) {
		return nil
	}
	return scalar(fd, typ, m.Get(fd))
}

func scalar(fd protoreflect.FieldDescriptor, typ Type, v protoreflect.Value) any {
	switch typ {
	case TypeString:
		if s := v.String(); s != "" {
			return s
		}
		return nil
	case TypeEnum:
		if v.Enum() == 0 {
			return nil
		}
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return string(ev.Name())
		}
		return nil
	case TypeBool:
		return v.Bool()
	case TypeFloat:
		return v.Float()
	case TypeInt:
		switch fd.Kind() {
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			return int64(min(v.Uint(), math.MaxInt64))
		}
		return v.Int()
	case TypeTime, TypeDuration:
		m := v.Message()
		fields := m.Descriptor().Fields()
		sec := m.Get(fields.ByName("seconds")).Int()
		nano := m.Get(fields.ByName("nanos")).Int()
		if typ == TypeTime {
			return time.Unix(sec, nano).UTC()
		}
		return time.Duration(sec)*time.Second + time.Duration(nano)
	}
	return nil
}

// coerce turns a value a filter compares with into the column's type: a text the command line typed, or a Go value.
// An enum takes its value's name, whole or without its prefix, in any case, or its number.
func coerce(col Column, v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("%s: a null value: filter with IsNull", col.Ref)
	}
	bad := func() (any, error) { return nil, fmt.Errorf("%s: %v (%T) is not a %s", col.Ref, v, v, col.Type) }
	s, isText := v.(string)
	switch col.Type {
	case TypeString:
		if !isText {
			return bad()
		}
		return s, nil
	case TypeEnum:
		values := col.Field.Enum().Values()
		if isText {
			prefix := enumPrefix(col.Field.Enum())
			for i := range values.Len() {
				name := string(values.Get(i).Name())
				if strings.EqualFold(s, name) || strings.EqualFold(prefix+s, name) {
					return name, nil
				}
			}
			return nil, fmt.Errorf("%s: %s is not a value of %s", col.Ref, s, col.Field.Enum().FullName())
		}
		if n, ok := integer(v); ok {
			if ev := values.ByNumber(protoreflect.EnumNumber(n)); ev != nil {
				return string(ev.Name()), nil
			}
		}
		return bad()
	case TypeInt:
		if isText {
			n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil {
				return bad()
			}
			return n, nil
		}
		if n, ok := integer(v); ok {
			return n, nil
		}
		return bad()
	case TypeFloat:
		if isText {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return bad()
			}
			return f, nil
		}
		if f, ok := v.(float64); ok {
			return f, nil
		}
		if n, ok := integer(v); ok {
			return float64(n), nil
		}
		return bad()
	case TypeBool:
		if isText {
			b, err := strconv.ParseBool(strings.TrimSpace(s))
			if err != nil {
				return bad()
			}
			return b, nil
		}
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return bad()
	case TypeTime:
		if t, ok := v.(time.Time); ok {
			return t.UTC(), nil
		}
		if isText {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC(), nil
			}
			// A day alone starts at midnight on the machine, as the window shows days.
			if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
				return t.UTC(), nil
			}
		}
		return bad()
	case TypeDuration:
		if d, ok := v.(time.Duration); ok {
			return d, nil
		}
		if isText {
			if d, err := time.ParseDuration(s); err == nil {
				return d, nil
			}
		}
		return bad()
	}
	return bad()
}

// integer reads v as an int64 when it is a Go integer, or a float without a fraction.
func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint32:
		return int64(n), true
	case float64:
		if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
			return int64(n), true
		}
	}
	return 0, false
}

// compare orders two values of col, neither nil: text ignoring case first, an enum by its number.
func compare(col Column, a, b any) int {
	switch col.Type {
	case TypeString:
		x, y := a.(string), b.(string)
		if c := strings.Compare(strings.ToLower(x), strings.ToLower(y)); c != 0 {
			return c
		}
		return strings.Compare(x, y)
	case TypeEnum:
		values := col.Field.Enum().Values()
		number := func(v any) protoreflect.EnumNumber {
			if ev := values.ByName(protoreflect.Name(v.(string))); ev != nil {
				return ev.Number()
			}
			return 0
		}
		return cmp.Compare(number(a), number(b))
	case TypeInt:
		return cmp.Compare(a.(int64), b.(int64))
	case TypeFloat:
		return cmp.Compare(a.(float64), b.(float64))
	case TypeBool:
		x, y := a.(bool), b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	case TypeTime:
		return a.(time.Time).Compare(b.(time.Time))
	case TypeDuration:
		return cmp.Compare(a.(time.Duration), b.(time.Duration))
	}
	return 0
}

// equal tells whether two values of col are the same: text ignoring case, as the store compares it.
func equal(col Column, a, b any) bool {
	if col.Type == TypeString {
		return strings.EqualFold(a.(string), b.(string))
	}
	return compare(col, a, b) == 0
}

// Package querytext reads the texts a person types for a query into QueryService's typed messages, and writes them
// back: a filter status=running, a sort -create_time, a group create_time::week, a measure cost=usage.cost_usd::sum.
// QueryService only reads typed messages; the command line and djinn mcp read their texts here, on the caller's side,
// a filter typed by the ref it reads, which the collection's schema gives.
package querytext

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
)

// ErrInvalid wraps the error of a text that does not read.
var ErrInvalid = errors.New("invalid query text")

// The grammars of the texts, as the help of the command line says them.
const (
	FilterHelp = "a filter: ref, an operator, a value, read with the type of the ref. = is equal, text ignoring case, " +
		"and a|b one of; != is none of, an absent value included; > >= < <= bound a number, a time (2026-11-01, in " +
		"this machine's time zone, or RFC 3339) or a duration (90s); *= contains, ^= begins with, ignoring case; " +
		"null is the absent value: status=null, status!=null, status=pending|null. An enum takes its value's name, " +
		"prefix or not: status=running"
	OrderHelp = "a sort: ref ascending, -ref descending; aggregated, a column of the table: -count"
	GroupHelp = "a group: ref, ref::day, ref::week, ref::month, or ref::bins(b1,b2,...) for a number or a " +
		"duration: answer::delay::bins(0s,1m,10m,1h)"
	MeasureHelp = "a measure: count, ref::fn with fn count, sum, avg, min, max, median or p90, or name=ref::fn to " +
		"name its column: running::median"
)

// Schema gives the refs of a collection, as QueryService.Schema does.
type Schema func(ctx context.Context, c queryv1.Collection) ([]*queryv1.QueryColumn, error)

// Types gives the type of a ref, reading each collection's schema once.
type Types struct {
	schema Schema
	cols   map[queryv1.Collection]map[string]*queryv1.QueryColumn
}

// NewTypes reads the types of refs through schema.
func NewTypes(schema Schema) *Types {
	return &Types{schema: schema, cols: map[queryv1.Collection]map[string]*queryv1.QueryColumn{}}
}

// Of is the type of ref in the collection c: a field, a ref Djinn computes, or one read across a jump (wish_id->title).
func (t *Types) Of(ctx context.Context, c queryv1.Collection, ref string) (queryv1.ColumnType, error) {
	cols, ok := t.cols[c]
	if !ok {
		list, err := t.schema(ctx, c)
		if err != nil {
			return 0, err
		}
		cols = make(map[string]*queryv1.QueryColumn, len(list))
		for _, col := range list {
			cols[col.GetRef()] = col
		}
		t.cols[c] = cols
	}
	head, rest, crosses := strings.Cut(ref, "->")
	col, ok := cols[head]
	if !ok {
		return 0, fmt.Errorf("%w: %s has no ref %s", ErrInvalid, collectionName(c), head)
	}
	if !crosses {
		return col.GetType(), nil
	}
	if col.GetJump() == queryv1.Collection_COLLECTION_UNSPECIFIED {
		return 0, fmt.Errorf("%w: %s holds no id to read %s across", ErrInvalid, head, rest)
	}
	return t.Of(ctx, col.GetJump(), rest)
}

func collectionName(c queryv1.Collection) string {
	return strings.ToLower(strings.TrimPrefix(c.String(), "COLLECTION_"))
}

// null is the value that stands for an absent one in a filter's text.
const null = "null"

// ops are a filter's operators, the longer first where one starts another.
var ops = []string{"!=", ">=", "<=", "*=", "^=", "=", ">", "<"}

var compareOps = map[string]queryv1.CompareOp{
	">": queryv1.CompareOp_COMPARE_OP_GREATER_THAN, ">=": queryv1.CompareOp_COMPARE_OP_GREATER_THAN_EQUAL,
	"<": queryv1.CompareOp_COMPARE_OP_LESS_THAN, "<=": queryv1.CompareOp_COMPARE_OP_LESS_THAN_EQUAL,
}

// Filter reads a filter of the collection c, typed by its ref's type.
func Filter(ctx context.Context, s string, c queryv1.Collection, types *Types) (*queryv1.Filter, error) {
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
	typ, err := types.Of(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	switch op {
	case "=", "!=":
		f, err := oneOf(ref, typ, strings.Split(value, "|"))
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", s, err)
		}
		if op == "!=" {
			f = not(f)
		}
		return f, nil
	case "*=", "^=":
		if typ != queryv1.ColumnType_COLUMN_TYPE_STRING && typ != queryv1.ColumnType_COLUMN_TYPE_ENUM {
			return nil, fmt.Errorf("%w: filter %q: %s is %s, not a text", ErrInvalid, s, ref, typeName(typ))
		}
		sop := queryv1.StringOp_STRING_OP_CONTAINS
		if op == "^=" {
			sop = queryv1.StringOp_STRING_OP_BEGINS_WITH
		}
		return &queryv1.Filter{Where: &queryv1.Filter_String_{String_: &queryv1.StringComparison{
			Ref: ref, Op: sop, Value: value}}}, nil
	}
	f, err := compare(ref, typ, compareOps[op], value)
	if err != nil {
		return nil, fmt.Errorf("filter %q: %w", s, err)
	}
	return f, nil
}

// oneOf is the filter that holds when ref's value is one of values, null among them for an absent one.
func oneOf(ref string, typ queryv1.ColumnType, values []string) (*queryv1.Filter, error) {
	includeNull := false
	var present []string
	for _, v := range values {
		if v == null {
			includeNull = true
			continue
		}
		present = append(present, v)
	}
	isNull := &queryv1.Filter{Where: &queryv1.Filter_Null{Null: &queryv1.NullFilter{Ref: ref}}}
	if len(present) == 0 {
		return isNull, nil
	}
	if typ == queryv1.ColumnType_COLUMN_TYPE_STRING || typ == queryv1.ColumnType_COLUMN_TYPE_ENUM {
		if len(present) == 1 && !includeNull {
			// An equality, which the server narrows its rows by when the field is indexed.
			return &queryv1.Filter{Where: &queryv1.Filter_String_{String_: &queryv1.StringComparison{
				Ref: ref, Op: queryv1.StringOp_STRING_OP_EQUAL, Value: present[0]}}}, nil
		}
		return &queryv1.Filter{Where: &queryv1.Filter_StringIn{StringIn: &queryv1.StringIn{
			Ref: ref, Values: present, IncludeNull: includeNull}}}, nil
	}
	var alts []*queryv1.Filter
	for _, v := range present {
		f, err := compare(ref, typ, queryv1.CompareOp_COMPARE_OP_EQUAL, v)
		if err != nil {
			return nil, err
		}
		alts = append(alts, f)
	}
	if includeNull {
		alts = append(alts, isNull)
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &queryv1.Filter{Where: &queryv1.Filter_Group{Group: &queryv1.FilterGroup{
		Op: queryv1.FilterGroupOp_FILTER_GROUP_OP_OR, Filters: alts}}}, nil
}

func not(f *queryv1.Filter) *queryv1.Filter {
	return &queryv1.Filter{Where: &queryv1.Filter_Group{Group: &queryv1.FilterGroup{
		Op: queryv1.FilterGroupOp_FILTER_GROUP_OP_NOT, Filters: []*queryv1.Filter{f}}}}
}

// compare is the comparison of ref, of type typ, with the value v reads as.
func compare(ref string, typ queryv1.ColumnType, op queryv1.CompareOp, v string) (*queryv1.Filter, error) {
	switch typ {
	case queryv1.ColumnType_COLUMN_TYPE_INT, queryv1.ColumnType_COLUMN_TYPE_FLOAT:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %q is not a number", ErrInvalid, ref, v)
		}
		return &queryv1.Filter{Where: &queryv1.Filter_Number{Number: &queryv1.NumberComparison{
			Ref: ref, Op: op, Value: n}}}, nil
	case queryv1.ColumnType_COLUMN_TYPE_TIME:
		t, err := Time(v)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, ref, err)
		}
		return &queryv1.Filter{Where: &queryv1.Filter_Time{Time: &queryv1.TimeComparison{
			Ref: ref, Op: op, Value: timestamppb.New(t)}}}, nil
	case queryv1.ColumnType_COLUMN_TYPE_DURATION:
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %q is not a duration", ErrInvalid, ref, v)
		}
		return &queryv1.Filter{Where: &queryv1.Filter_Duration{Duration: &queryv1.DurationComparison{
			Ref: ref, Op: op, Value: durationpb.New(d)}}}, nil
	case queryv1.ColumnType_COLUMN_TYPE_BOOL:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil || op != queryv1.CompareOp_COMPARE_OP_EQUAL {
			return nil, fmt.Errorf("%w: %s: true or false, compared by = only", ErrInvalid, ref)
		}
		return &queryv1.Filter{Where: &queryv1.Filter_Bool{Bool: &queryv1.BoolComparison{Ref: ref, Value: b}}}, nil
	}
	return nil, fmt.Errorf("%w: %s is %s: > >= < <= bound a number, a time or a duration", ErrInvalid, ref, typeName(typ))
}

// Time reads an instant: RFC 3339, or a day alone, which starts at midnight on this machine, as the window shows days.
func Time(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time: 2026-11-01 or RFC 3339", s)
}

func typeName(t queryv1.ColumnType) string {
	return "a " + strings.ToLower(strings.TrimPrefix(t.String(), "COLUMN_TYPE_"))
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

// Order reads a sort: ref ascending, -ref descending.
func Order(s string) (*queryv1.Order, error) {
	ref, desc := strings.CutPrefix(strings.TrimSpace(s), "-")
	if ref == "" {
		return nil, fmt.Errorf("%w: sort %q: no ref", ErrInvalid, s)
	}
	return &queryv1.Order{Ref: ref, Desc: desc}, nil
}

var periods = map[string]queryv1.Period{
	"day": queryv1.Period_PERIOD_DAY, "week": queryv1.Period_PERIOD_WEEK, "month": queryv1.Period_PERIOD_MONTH,
}

// Group reads a group of the collection c: ref, ref::day, ref::week, ref::month, or ref::bins(b1,b2,...), whose
// bounds read as the ref's type, a number or a duration.
func Group(ctx context.Context, s string, c queryv1.Collection, types *Types) (*queryv1.GroupBy, error) {
	s = strings.TrimSpace(s)
	if head, bounds, ok := strings.Cut(s, "::bins("); ok {
		inner, closed := strings.CutSuffix(bounds, ")")
		if !closed || head == "" {
			return nil, fmt.Errorf("%w: group %q: write ref::bins(b1,b2,...)", ErrInvalid, s)
		}
		typ, err := types.Of(ctx, c, head)
		if err != nil {
			return nil, err
		}
		var numbers []float64
		var durations []*durationpb.Duration
		for b := range strings.SplitSeq(inner, ",") {
			if b = strings.TrimSpace(b); b == "" {
				return nil, fmt.Errorf("%w: group %q: an empty bound", ErrInvalid, s)
			}
			switch typ {
			case queryv1.ColumnType_COLUMN_TYPE_INT, queryv1.ColumnType_COLUMN_TYPE_FLOAT:
				n, err := strconv.ParseFloat(b, 64)
				if err != nil {
					return nil, fmt.Errorf("%w: group %q: %q is not a number", ErrInvalid, s, b)
				}
				numbers = append(numbers, n)
			case queryv1.ColumnType_COLUMN_TYPE_DURATION:
				d, err := time.ParseDuration(b)
				if err != nil {
					return nil, fmt.Errorf("%w: group %q: %q is not a duration", ErrInvalid, s, b)
				}
				durations = append(durations, durationpb.New(d))
			default:
				return nil, fmt.Errorf("%w: group %q: %s is %s, which has no bins", ErrInvalid, s, head, typeName(typ))
			}
		}
		if durations != nil {
			return &queryv1.GroupBy{Ref: head, Bucket: &queryv1.GroupBy_DurationBins{
				DurationBins: &queryv1.DurationBins{Bounds: durations}}}, nil
		}
		return &queryv1.GroupBy{Ref: head, Bucket: &queryv1.GroupBy_NumberBins{
			NumberBins: &queryv1.NumberBins{Bounds: numbers}}}, nil
	}
	for name, p := range periods {
		if head, ok := strings.CutSuffix(s, "::"+name); ok && head != "" {
			return &queryv1.GroupBy{Ref: head, Bucket: &queryv1.GroupBy_Period{Period: p}}, nil
		}
	}
	if s == "" {
		return nil, fmt.Errorf("%w: an empty group", ErrInvalid)
	}
	return &queryv1.GroupBy{Ref: s}, nil
}

// Measure reads a measure: count, ref::fn, or name=ref::fn (name=count) to name its column.
func Measure(s string) (*queryv1.Measure, error) {
	m := &queryv1.Measure{}
	body := strings.TrimSpace(s)
	if alias, rest, ok := strings.Cut(body, "="); ok {
		m.Name, body = strings.TrimSpace(alias), strings.TrimSpace(rest)
		if m.Name == "" {
			return nil, fmt.Errorf("%w: measure %q: an empty name", ErrInvalid, s)
		}
	}
	if body == "count" {
		m.Fn = queryv1.MeasureFn_MEASURE_FN_COUNT
		return m, nil
	}
	i := strings.LastIndex(body, "::")
	if i <= 0 {
		return nil, fmt.Errorf("%w: measure %q: write count, or ref::fn", ErrInvalid, s)
	}
	m.Ref = body[:i]
	fn, ok := queryv1.MeasureFn_value["MEASURE_FN_"+strings.ToUpper(body[i+2:])]
	if !ok || fn == 0 {
		return nil, fmt.Errorf("%w: measure %q: no function %s: count sum avg min max median p90", ErrInvalid, s,
			body[i+2:])
	}
	m.Fn = queryv1.MeasureFn(fn)
	return m, nil
}

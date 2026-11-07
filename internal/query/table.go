package query

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The units a column's numbers come in, by the suffix of its field's name.
const (
	usd     = "usd"
	tokens  = "tokens"
	bytesU  = "bytes"
	percent = "percent"
	seconds = "seconds"
)

// unit is the unit of fd's numbers, by the suffix of its name: cost_usd is in usd, input_tokens in tokens.
func unit(fd protoreflect.FieldDescriptor) string {
	if k := fd.Kind(); k == protoreflect.StringKind || k == protoreflect.BoolKind || k == protoreflect.EnumKind ||
		k == protoreflect.MessageKind {
		return ""
	}
	name := "_" + string(fd.Name())
	for suffix, u := range map[string]string{"_usd": usd, "_tokens": tokens, "_bytes": bytesU, "_percent": percent,
		"_seconds": seconds} {
		if strings.HasSuffix(name, suffix) {
			return u
		}
	}
	return ""
}

// Label is a short name of col for a person, in English, from its ref: Wish title for wish_id->title, Running
// (median) for running::median.
func Label(col Column) string {
	switch {
	case col.Fn == Count && col.Of == "" && col.Ref == Count.String():
		return "Count"
	case col.Fn != 0 && col.Ref == col.Of+"::"+col.Fn.String():
		return words(col.Of) + " (" + col.Fn.String() + ")"
	case col.Granularity != 0:
		return words(col.Of) + " by " + col.Granularity.String()
	case col.Bounds != nil:
		return words(col.Of)
	}
	return words(col.Ref)
}

// words turns a ref into words, its first letter upper.
func words(ref string) string {
	s := strings.NewReplacer("_id->", " ", "->", " ", "::", " ", ".", " ", "_", " ").Replace(ref)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// WriteText writes t aligned, a line per row under a line of refs, then how many rows it holds. A value is written as
// in WriteCSV.
func (t *Table) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	line := func(cells []string) {
		for i, c := range cells {
			cells[i] = strings.Map(func(r rune) rune {
				if r == '\t' || r == '\n' || r == '\r' {
					return ' '
				}
				return r
			}, c)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	refs := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		refs[i] = c.Ref
	}
	line(refs)
	for _, row := range t.Rows {
		line(t.cells(row))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	n := fmt.Sprintf("%d rows", len(t.Rows))
	if len(t.Rows) == 1 {
		n = "1 row"
	}
	if t.Total > len(t.Rows) {
		n = fmt.Sprintf("%d of %d rows", len(t.Rows), t.Total)
	}
	_, err := fmt.Fprintln(w, n)
	return err
}

// WriteCSV writes t as CSV: a header of refs, then a line per row. An absent value is an empty cell; an enum is its
// value's name without its prefix, in lower case (running); a time is RFC 3339 in UTC; a duration is Go's without its
// trailing zero units (1h30m, 1h); a list joins its values with ", "; a bin is its range (< 10s, 10s to 1m, >= 1h).
func (t *Table) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	refs := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		refs[i] = c.Ref
	}
	if err := cw.Write(refs); err != nil {
		return err
	}
	for _, row := range t.Rows {
		if err := cw.Write(t.cells(row)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func (t *Table) cells(row []any) []string {
	out := make([]string, len(row))
	for i, v := range row {
		col := t.Columns[i]
		if col.Bounds != nil && v != nil {
			out[i] = binText(col, v)
			continue
		}
		if l, ok := v.([]any); ok {
			parts := make([]string, len(l))
			for j, x := range l {
				parts[j] = cell(col, x)
			}
			out[i] = strings.Join(parts, ", ")
			continue
		}
		out[i] = cell(col, v)
	}
	return out
}

// cell writes one value of col.
func cell(col Column, v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if col.Type == TypeEnum && col.Field != nil {
			return strings.ToLower(strings.TrimPrefix(x, enumPrefix(col.Field.Enum())))
		}
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case time.Duration:
		return duration(x)
	}
	return fmt.Sprint(v)
}

// duration writes d as Go does, to the millisecond, without its trailing zero units: 1h, 1m30s, 250ms.
func duration(d time.Duration) string {
	s := d.Round(time.Millisecond).String()
	if t, ok := strings.CutSuffix(s, "m0s"); ok {
		s = t + "m"
	}
	if t, ok := strings.CutSuffix(s, "h0m"); ok {
		s = t + "h"
	}
	return s
}

// binText writes the range of the bin whose bound is v: below the second bound for the first, from the last bound up
// for the last.
func binText(col Column, v any) string {
	b := col.Bounds
	i := 0
	for i < len(b) && compare(col, b[i], v) != 0 {
		i++
	}
	switch {
	case i == 0:
		return "< " + cell(col, b[1])
	case i >= len(b)-1:
		return ">= " + cell(col, b[len(b)-1])
	}
	return cell(col, b[i]) + " to " + cell(col, b[i+1])
}

// Meta is the "djinn" metadata of each field of a table in Arrow, as JSON.
type Meta struct {
	Ref   string `json:"ref"`
	Label string `json:"label"`
	// Unit is the unit of the numbers, when they have one: usd, tokens.
	Unit string `json:"unit,omitempty"`
	// Type is Djinn's type of the column: string, enum, int, float, bool, time or duration.
	Type string `json:"type"`
	List bool   `json:"list,omitempty"`
	// Of, Fn, Granularity and Bounds are an aggregated column's: the ref it reads, its function, the period of a group
	// of times, the bounds of a group's bins (durations in milliseconds, as the column holds them).
	Of          string `json:"of,omitempty"`
	Fn          string `json:"fn,omitempty"`
	Granularity string `json:"granularity,omitempty"`
	Bounds      []any  `json:"bounds,omitempty"`
}

// MetaKey is the key of a field's metadata in Arrow.
const MetaKey = "djinn"

func meta(col Column) Meta {
	m := Meta{Ref: col.Ref, Label: Label(col), Unit: col.Unit, Type: col.Type.String(), List: col.List, Of: col.Of,
		Fn: col.Fn.String(), Granularity: col.Granularity.String()}
	for _, b := range col.Bounds {
		if d, ok := b.(time.Duration); ok {
			b = d.Milliseconds()
		}
		m.Bounds = append(m.Bounds, b)
	}
	return m
}

// arrowType is the Arrow type of a value of typ: a time in milliseconds in UTC, a duration in milliseconds, an enum
// a dictionary of its names.
func arrowType(typ Type) arrow.DataType {
	switch typ {
	case TypeEnum:
		return &arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int32, ValueType: arrow.BinaryTypes.String}
	case TypeInt:
		return arrow.PrimitiveTypes.Int64
	case TypeFloat:
		return arrow.PrimitiveTypes.Float64
	case TypeBool:
		return arrow.FixedWidthTypes.Boolean
	case TypeTime:
		return &arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: "UTC"}
	case TypeDuration:
		return &arrow.DurationType{Unit: arrow.Millisecond}
	}
	return arrow.BinaryTypes.String
}

// Arrow writes t as an Arrow IPC stream of one record batch: a field per column, named by its ref, of the type its
// column has (a list of them for a list, of names for a list of enums), each holding its Meta. An absent value is
// null, never 0.
func (t *Table) Arrow() ([]byte, error) {
	fields := make([]arrow.Field, len(t.Columns))
	for i, col := range t.Columns {
		typ := arrowType(col.Type)
		if col.List {
			if col.Type == TypeEnum {
				typ = arrow.BinaryTypes.String
			}
			typ = arrow.ListOf(typ)
		}
		m, err := json.Marshal(meta(col))
		if err != nil {
			return nil, err
		}
		fields[i] = arrow.Field{Name: col.Ref, Type: typ, Nullable: true,
			Metadata: arrow.NewMetadata([]string{MetaKey}, []string{string(m)})}
	}
	schema := arrow.NewSchema(fields, nil)
	mem := memory.DefaultAllocator
	b := array.NewRecordBuilder(mem, schema)
	defer b.Release()
	for i, col := range t.Columns {
		fb := b.Field(i)
		for _, row := range t.Rows {
			if err := appendValue(fb, col, row[i]); err != nil {
				return nil, fmt.Errorf("column %s: %w", col.Ref, err)
			}
		}
	}
	rec := b.NewRecordBatch()
	defer rec.Release()
	var out bytes.Buffer
	w := ipc.NewWriter(&out, ipc.WithSchema(schema), ipc.WithAllocator(mem))
	if err := w.Write(rec); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// appendValue appends v, a value of col or nil, to its field's builder.
func appendValue(fb array.Builder, col Column, v any) error {
	if v == nil {
		fb.AppendNull()
		return nil
	}
	if col.List {
		lb, ok := fb.(*array.ListBuilder)
		values, isList := v.([]any)
		if !ok || !isList {
			return fmt.Errorf("%T is not a list", v)
		}
		lb.Append(true)
		item := col
		item.List = false
		if col.Type == TypeEnum {
			item.Type = TypeString
		}
		for _, x := range values {
			if err := appendValue(lb.ValueBuilder(), item, x); err != nil {
				return err
			}
		}
		return nil
	}
	bad := func() error { return fmt.Errorf("%T is not a %s", v, col.Type) }
	switch b := fb.(type) {
	case *array.BinaryDictionaryBuilder:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		return b.AppendString(s)
	case *array.StringBuilder:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		b.Append(s)
	case *array.Int64Builder:
		n, ok := v.(int64)
		if !ok {
			return bad()
		}
		b.Append(n)
	case *array.Float64Builder:
		f, ok := v.(float64)
		if !ok {
			return bad()
		}
		b.Append(f)
	case *array.BooleanBuilder:
		x, ok := v.(bool)
		if !ok {
			return bad()
		}
		b.Append(x)
	case *array.TimestampBuilder:
		x, ok := v.(time.Time)
		if !ok {
			return bad()
		}
		b.Append(arrow.Timestamp(x.UnixMilli()))
	case *array.DurationBuilder:
		x, ok := v.(time.Duration)
		if !ok {
			return bad()
		}
		b.Append(arrow.Duration(x.Milliseconds()))
	default:
		return fmt.Errorf("no builder for %T", fb)
	}
	return nil
}

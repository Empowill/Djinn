// Package query reads a collection of stored messages by refs, as a table: the columns a query selects, its rows
// filtered and sorted. A ref names a field by its path in the message (usage.cost_usd) and may cross to another
// collection through a field that holds an id (wish_id->title), as its (djinn.v1.ref) option declares. The schema
// comes from the descriptor: a column's type never depends on the values a row holds. A collection adds refs computed
// in Go (prompt::size), and a query may aggregate its rows: groups by refs, a time truncated to its day, week or month,
// and measures named ref::fn (running::median).
//
// The store keeps each entity as a protobuf payload, its *_id fields aside as columns: an equality on one of them
// narrows the rows SQLite reads, and everything else runs in Go, on the decoded messages.
//
// A table writes itself in Arrow, typed, for the window, or as aligned text and CSV, for the command line; Handler
// serves Djinn's collections as QueryService, its filters, sorts, groups and measures read from their texts.
package query

import (
	"errors"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// ErrInvalid wraps the errors of a query that cannot run as written: an unknown or hidden ref, a value of the
// wrong type, an empty group.
var ErrInvalid = errors.New("invalid query")

// Type is the type of a column, taken from the descriptor.
type Type int

// The types of a column. A value is nil when absent, or else a string (TypeString, and TypeEnum by its value's
// name), an int64, a float64, a bool, a time.Time in UTC or a time.Duration.
const (
	TypeString Type = iota + 1
	TypeEnum
	TypeInt
	TypeFloat
	TypeBool
	TypeTime
	TypeDuration
)

func (t Type) String() string {
	switch t {
	case TypeString:
		return "string"
	case TypeEnum:
		return "enum"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "bool"
	case TypeTime:
		return "time"
	case TypeDuration:
		return "duration"
	}
	return "unknown"
}

// Column is one ref of a collection.
type Column struct {
	// Ref is how a query names it: code, usage.cost_usd, wish_id->title.
	Ref  string
	Type Type
	// List is a repeated field: its value is a []any of Type, nil when empty.
	List bool
	// Field is the field read last, past the jumps: its descriptor holds its comment, its enum, its options. It is
	// nil for a derived ref, and for a count of rows.
	Field protoreflect.FieldDescriptor
	// Jump is the full name of the message whose id the field holds, by its (djinn.v1.ref) option: the ref
	// Ref+"->"+r reads r there.
	Jump protoreflect.FullName
	// Doc says what a derived ref is.
	Doc string
	// Unit is the unit of its numbers, when they have one: usd, tokens. A time or a duration has none: its type says
	// it.
	Unit string

	// In an aggregated table, Of is the ref a group or a measure reads ("" for a count of rows), Fn the measure's
	// function, Granularity the period a group of times truncates them to, and Bounds the bounds of a group's bins,
	// in Type.
	Of          string
	Fn          Fn
	Granularity Granularity
	Bounds      []any
}

// maxDepth bounds how deep Schema walks nested messages.
const maxDepth = 4

// Schema gives the refs of md, flattened: a nested message's fields as path.field, never a hidden field, nor a map,
// nor a repeated message, nor a message that holds itself. Jumps are not followed: Column.Jump names their target.
func Schema(md protoreflect.MessageDescriptor) []Column {
	var out []Column
	walk(md, "", []protoreflect.FullName{md.FullName()}, &out)
	return out
}

func walk(md protoreflect.MessageDescriptor, prefix string, path []protoreflect.FullName, out *[]Column) {
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if hidden(fd) || fd.IsMap() {
			continue
		}
		ref := prefix + string(fd.Name())
		if typ, ok := leaf(fd); ok {
			*out = append(*out, Column{Ref: ref, Type: typ, List: fd.IsList(), Field: fd, Jump: jump(fd), Unit: unit(fd)})
			continue
		}
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || len(path) >= maxDepth {
			continue
		}
		if slices.Contains(path, fd.Message().FullName()) {
			continue
		}
		walk(fd.Message(), ref+".", append(slices.Clone(path), fd.Message().FullName()), out)
	}
}

// leaf gives the type of a field read as one value: a scalar, an enum, a timestamp or a duration.
func leaf(fd protoreflect.FieldDescriptor) (Type, bool) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return TypeString, true
	case protoreflect.EnumKind:
		return TypeEnum, true
	case protoreflect.BoolKind:
		return TypeBool, true
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return TypeInt, true
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return TypeFloat, true
	case protoreflect.MessageKind:
		switch fd.Message().FullName() {
		case "google.protobuf.Timestamp":
			return TypeTime, true
		case "google.protobuf.Duration":
			return TypeDuration, true
		}
	}
	return 0, false
}

func hidden(fd protoreflect.FieldDescriptor) bool {
	q, _ := proto.GetExtension(fd.Options(), djinnv1.E_Query).(djinnv1.Query)
	return q == djinnv1.Query_QUERY_HIDDEN
}

// jump is the target of a singular string field's (djinn.v1.ref) option.
func jump(fd protoreflect.FieldDescriptor) protoreflect.FullName {
	if fd.Kind() != protoreflect.StringKind || fd.IsList() {
		return ""
	}
	ref, _ := proto.GetExtension(fd.Options(), djinnv1.E_Ref).(string)
	return protoreflect.FullName(ref)
}

// enumPrefix is the prefix every value name of ed holds by convention: TASK_STATUS_ for TaskStatus.
func enumPrefix(ed protoreflect.EnumDescriptor) string {
	var b strings.Builder
	name := string(ed.Name())
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String()) + "_"
}

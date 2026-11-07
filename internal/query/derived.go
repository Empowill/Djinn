package query

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Derived is a ref a collection computes in Go, where no field holds the value: the size of a hidden text, the time
// between two of its fields. A query reads it as any ref, across a jump too (wait_question_id->answer::delay). Its name
// holds :: by convention, after the field it reads: prompt::size.
type Derived struct {
	Ref  string
	Type Type
	// Doc says what it is, as a field's comment would.
	Doc string
	// Unit is the unit of its numbers, when they have one (WithUnit).
	Unit string
	of   protoreflect.FullName
	get  func(proto.Message) any
}

// Derive declares a derived ref of the collection whose messages are M's. get gives a value of typ, as Column says,
// or nil when absent: never a zero for a value it cannot compute.
func Derive[M proto.Message](ref string, typ Type, doc string, get func(M) any) Derived {
	var zero M
	return Derived{Ref: ref, Type: typ, Doc: doc, of: zero.ProtoReflect().Descriptor().FullName(),
		get: func(m proto.Message) any {
			if x, ok := m.(M); ok {
				return get(x)
			}
			return nil
		}}
}

func (d Derived) column() Column { return Column{Ref: d.Ref, Type: d.Type, Doc: d.Doc, Unit: d.Unit} }

// WithUnit gives d the unit of its numbers.
func (d Derived) WithUnit(unit string) Derived {
	d.Unit = unit
	return d
}

// check refuses a derived ref of another message than md, or whose name a query could not read.
func (d Derived) check(md protoreflect.MessageDescriptor) error {
	switch {
	case d.get == nil:
		return fmt.Errorf("derived ref %q: not declared by Derive", d.Ref)
	case d.of != md.FullName():
		return fmt.Errorf("derived ref %q reads %s, not %s", d.Ref, d.of, md.FullName())
	case d.Ref == "" || strings.Contains(d.Ref, "->"):
		return fmt.Errorf("derived ref %q: a name without ->", d.Ref)
	case d.Type < TypeString || d.Type > TypeDuration || d.Type == TypeEnum:
		return fmt.Errorf("derived ref %q: no type, or an enum, which has no descriptor here", d.Ref)
	}
	if _, _, err := path(md, d.Ref, false); err == nil {
		return fmt.Errorf("derived ref %q: a field's ref already", d.Ref)
	}
	return nil
}

// TaskRefs are the derived refs of the tasks.
var TaskRefs = []Derived{
	Derive("prompt::size", TypeInt, "The prompt's size in tokens, estimated as its characters divided by 4, rounded up: "+
		"the prompt itself stays hidden.", func(t *planv1.Task) any { return size(t.GetPrompt()) }).WithUnit(tokens),
	Derive("integration::delay", TypeDuration, "From the task's end to its work's commit into its wish's integration "+
		"branch (integration.update_time - end_time), for a work committed only.", func(t *planv1.Task) any {
		if t.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			return nil
		}
		return between(t.GetEndTime(), t.GetIntegration().GetUpdateTime())
	}),
}

// QuestionRefs are the derived refs of the questions.
var QuestionRefs = []Derived{
	Derive("context::size", TypeInt, "The context's size in tokens, estimated as its characters divided by 4, "+
		"rounded up: the context itself stays hidden.",
		func(q *planv1.Question) any { return size(q.GetContext()) }).WithUnit(tokens),
	Derive("answer.note::size", TypeInt, "The answer's note size in tokens, estimated as its characters divided by 4, "+
		"rounded up.", func(q *planv1.Question) any { return size(q.GetAnswer().GetNote()) }).WithUnit(tokens),
	Derive("answer::delay", TypeDuration, "How long the question waited for its answer (answer.create_time - "+
		"create_time), whoever answered.", func(q *planv1.Question) any {
		return between(q.GetCreateTime(), q.GetAnswer().GetCreateTime())
	}),
}

// size estimates the tokens of a text without a model, as its characters divided by 4, rounded up: nil for none.
func size(s string) any {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return nil
	}
	return int64((n + 3) / 4)
}

// between is the time from a to b: nil when one is absent, or b is before a, which no clock of Djinn's makes.
func between(a, b *timestamppb.Timestamp) any {
	if a == nil || b == nil {
		return nil
	}
	d := b.AsTime().Sub(a.AsTime())
	if d < 0 {
		return nil
	}
	return d
}

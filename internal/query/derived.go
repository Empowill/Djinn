package query

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
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
	// Enum is the enum of its values, for a TypeEnum (DeriveEnum).
	Enum protoreflect.EnumDescriptor
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

// DeriveEnum declares a derived ref whose values are those of the enum E: get gives one, never its zero, or nil.
func DeriveEnum[M proto.Message, E protoreflect.Enum](ref, doc string, get func(M) E) Derived {
	var zero E
	d := Derive(ref, TypeEnum, doc, func(m M) any {
		e := get(m)
		if e.Number() == 0 {
			return nil
		}
		if v := e.Descriptor().Values().ByNumber(e.Number()); v != nil {
			return string(v.Name())
		}
		return nil
	})
	d.Enum = zero.Descriptor()
	return d
}

func (d Derived) column() Column {
	return Column{Ref: d.Ref, Type: d.Type, Doc: d.Doc, Unit: d.Unit, Enum: d.Enum}
}

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
	case d.Type < TypeString || d.Type > TypeDuration:
		return fmt.Errorf("derived ref %q: no type", d.Ref)
	case (d.Type == TypeEnum) != (d.Enum != nil):
		return fmt.Errorf("derived ref %q: an enum, and only an enum, names its values (DeriveEnum)", d.Ref)
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
	DeriveEnum("type", "What the task is, for every task, where kind is set on a few: azima, watcher, ifrit, mockup, "+
		"investigation, review, correction, or else work.", taskType),
}

// taskType is what t is, the first that fits from azima down: its kind, its provider for a watcher, then what Djinn
// started it for, a review or a correction; work for any other.
func taskType(t *planv1.Task) queryv1.TaskType {
	switch {
	case t.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA:
		return queryv1.TaskType_TASK_TYPE_AZIMA
	case t.GetProvider() == planv1.Provider_PROVIDER_WATCH:
		return queryv1.TaskType_TASK_TYPE_WATCHER
	case t.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT:
		return queryv1.TaskType_TASK_TYPE_IFRIT
	case t.GetKind() == planv1.TaskKind_TASK_KIND_MOCKUP:
		return queryv1.TaskType_TASK_TYPE_MOCKUP
	case t.GetKind() == planv1.TaskKind_TASK_KIND_INVESTIGATION:
		return queryv1.TaskType_TASK_TYPE_INVESTIGATION
	case t.GetReview() != nil:
		return queryv1.TaskType_TASK_TYPE_REVIEW
	case t.GetCorrection() != nil:
		return queryv1.TaskType_TASK_TYPE_CORRECTION
	}
	return queryv1.TaskType_TASK_TYPE_WORK
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

// Timed gives the tasks of src with their times, which the store does not keep: running, waiting_gate, paused and
// waiting_before_start, as the task list computes them from their runs and gate waits as of now (plan.ReadTimes), an
// azima's summing its parts'. A task that never started has none but waiting_before_start: null, never 0.
func Timed(r store.Reader, src Source, now func() time.Time) Source {
	return &timed{Source: src, r: r, now: now}
}

type timed struct {
	Source
	r   store.Reader
	now func() time.Time
}

func (s *timed) Load(ctx context.Context, where map[string]string) ([]proto.Message, error) {
	msgs, err := s.Source.Load(ctx, where)
	if err != nil {
		return nil, err
	}
	tasks := make([]*planv1.Task, 0, len(msgs))
	for _, m := range msgs {
		if t, ok := m.(*planv1.Task); ok {
			tasks = append(tasks, t)
		}
	}
	if err := plan.ReadTimes(ctx, s.r, tasks, s.now()); err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.GetFirstStartTime() == nil {
			t.Running, t.WaitingGate, t.Paused = nil, nil, nil
		}
	}
	return msgs, nil
}

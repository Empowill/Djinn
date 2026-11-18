package store

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestInSubset pins that a subset reads, through its own index, the rows of many values in one statement per 500:
// the prompts of a page of tasks, where reading each task's events apart held the database 30 s (L25, W123). A row
// whose payload holds the marker's bytes in a text is not in the subset.
func TestInSubset(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.TaskEvent{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	prompt, result := planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT
	event := func(task string, seq int64, kind planv1.TaskEventKind, text string) *planv1.TaskEvent {
		return &planv1.TaskEvent{Id: NewID(), TaskId: task, Seq: seq, Kind: kind, Text: text}
	}
	a, b, c := NewID(), NewID(), NewID()
	// The prompt's marker is the bytes 0x20 0x01: b's tool result holds them in its text.
	if err := put(ctx, s,
		event(a, 1, prompt, "first"), event(a, 2, result, "ok"), event(b, 1, result, "a \x01 inside"),
		event(a, 3, prompt, "continue"), event(c, 1, result, "done"),
	); err != nil {
		t.Fatal(err)
	}
	if got := strs(t, s, `SELECT count(*) FROM task_event INDEXED BY task_event_subset_prompt
		WHERE instr(payload, X'2001') > 0`); got[0] != "3" {
		t.Errorf("index rows = %s; want 3, b's tool result among them", got[0])
	}
	asked := []string{strings.ToUpper(a), b, c}
	for i := range 1200 {
		asked = append(asked, fmt.Sprintf("00000000-0000-7000-8000-%012d", i))
	}
	var n atomic.Int64
	got, err := InSubset[*planv1.TaskEvent](ctx, Counted(s, &n), "prompt", asked)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, ev := range got {
		texts = append(texts, ev.GetText())
	}
	if !slices.Equal(texts, []string{"first", "continue"}) || n.Load() != 3 {
		t.Errorf("subset = %q in %d statements; want a's prompts in order, in 3 for 1 203 values", texts, n.Load())
	}
	if _, err := InSubset[*planv1.TaskEvent](ctx, s, "missing", asked); err == nil {
		t.Error("an unknown subset read; want an error")
	}
}

// TestSubsetMigrated pins that an index of a subset written otherwise, by an older Djinn, is replaced at Open.
func TestSubsetMigrated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, "", &planv1.TaskEvent{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := strs(t, s, `SELECT sql FROM sqlite_master WHERE name = 'task_event_subset_prompt'`)
	if len(want) != 1 || !strings.HasSuffix(want[0], `("task_id", "seq") WHERE instr(payload, X'2001') > 0`) {
		t.Fatalf("index = %q", want)
	}
	if _, err := s.db.ExecContext(ctx, `DROP INDEX task_event_subset_prompt`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX task_event_subset_prompt ON task_event (task_id)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx, "", []proto.Message{&planv1.TaskEvent{}}); err != nil {
		t.Fatal(err)
	}
	if got := strs(t, s, `SELECT sql FROM sqlite_master WHERE name = 'task_event_subset_prompt'`); !slices.Equal(got, want) {
		t.Errorf("index = %q; want %q", got, want)
	}
}

// TestNewSubset pins what a subset may declare: a value of an enum or a positive int64, ordered by columns.
func TestNewSubset(t *testing.T) {
	t.Parallel()
	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	field := func(name string, n int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(n), Label: optional, Type: typ.Enum()}
		if typ == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
			f.TypeName = proto.String(".test.v1.Kind")
		}
		return f
	}
	table := func(sub *djinnv1.Subset) (*table, error) {
		opts := &descriptorpb.MessageOptions{}
		proto.SetExtension(opts, djinnv1.E_Subset, []*djinnv1.Subset{sub})
		file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
			Name: proto.String("subset.proto"), Package: proto.String("test.v1"), Syntax: proto.String("proto3"),
			EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("Kind"), Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("KIND_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("KIND_PROMPT"), Number: proto.Int32(1)},
			}}},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("Event"), Options: opts, Field: []*descriptorpb.FieldDescriptorProto{
					field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("task_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("kind", 3, descriptorpb.FieldDescriptorProto_TYPE_ENUM),
					field("size", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("text", 5, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				},
			}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return newTable(file.Messages().Get(0))
	}
	by := []string{"task_id"}
	for _, tc := range []struct {
		name   string
		sub    *djinnv1.Subset
		marker string // empty for an error
	}{
		{"enum value by name", &djinnv1.Subset{Name: "prompt", Field: "kind", Value: "KIND_PROMPT", By: by}, "1801"},
		{"enum value by number", &djinnv1.Subset{Name: "prompt", Field: "kind", Value: "1", By: by}, "1801"},
		{"number", &djinnv1.Subset{Name: "big", Field: "size", Value: "300", By: by}, "20ac02"},
		{"no name", &djinnv1.Subset{Field: "kind", Value: "1", By: by}, ""},
		{"name not in lowercase", &djinnv1.Subset{Name: "Prompt", Field: "kind", Value: "1", By: by}, ""},
		{"missing field", &djinnv1.Subset{Name: "x", Field: "missing", Value: "1", By: by}, ""},
		{"text field", &djinnv1.Subset{Name: "x", Field: "text", Value: "1", By: by}, ""},
		{"no such value", &djinnv1.Subset{Name: "x", Field: "kind", Value: "KIND_OTHER", By: by}, ""},
		{"zero value", &djinnv1.Subset{Name: "x", Field: "kind", Value: "KIND_UNSPECIFIED", By: by}, ""},
		{"zero number", &djinnv1.Subset{Name: "x", Field: "size", Value: "0", By: by}, ""},
		{"no order", &djinnv1.Subset{Name: "x", Field: "kind", Value: "1"}, ""},
		{"order not indexed", &djinnv1.Subset{Name: "x", Field: "kind", Value: "1", By: []string{"text"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tbl, err := table(tc.sub)
			switch {
			case tc.marker == "" && err == nil:
				t.Errorf("subset %v accepted; want an error", tc.sub)
			case tc.marker != "" && err != nil:
				t.Errorf("subset %v: %v", tc.sub, err)
			case tc.marker != "" && fmt.Sprintf("%x", tbl.subsets[0].marker) != tc.marker:
				t.Errorf("marker = %x; want %s", tbl.subsets[0].marker, tc.marker)
			}
		})
	}
}

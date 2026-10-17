package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestNewTable(t *testing.T) {
	t.Parallel()

	makeDesc := func(t *testing.T, fields []*descriptorpb.FieldDescriptorProto, uniques ...*djinnv1.Unique) protoreflect.MessageDescriptor {
		t.Helper()
		opts := &descriptorpb.MessageOptions{}
		if len(uniques) > 0 {
			proto.SetExtension(opts, djinnv1.E_Unique, uniques)
		}
		fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
			Name:    proto.String("custom.proto"),
			Package: proto.String("test.v1"),
			Syntax:  proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{
				Name:    proto.String("Custom"),
				Field:   fields,
				Options: opts,
			}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return fd.Messages().Get(0)
	}

	strType := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	bytesType := descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
	optLabel := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repLabel := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	idField := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("id"), Number: proto.Int32(1), Label: optLabel, Type: strType, JsonName: proto.String("id"),
	}

	for _, tc := range []struct {
		name    string
		md      protoreflect.MessageDescriptor
		wantErr bool
	}{
		{
			name: "valid entity with unique and foreign key fields",
			md:   (&planv1.Question{}).ProtoReflect().Descriptor(),
		},
		{
			name:    "missing id field",
			md:      (&timestamppb.Timestamp{}).ProtoReflect().Descriptor(),
			wantErr: true,
		},
		{
			name: "empty unique group",
			md: makeDesc(t, []*descriptorpb.FieldDescriptorProto{idField},
				&djinnv1.Unique{}),
			wantErr: true,
		},
		{
			name: "unique field does not exist",
			md: makeDesc(t, []*descriptorpb.FieldDescriptorProto{idField},
				&djinnv1.Unique{Fields: []string{"missing"}}),
			wantErr: true,
		},
		{
			name: "repeated unique field rejected",
			md: makeDesc(t, []*descriptorpb.FieldDescriptorProto{
				idField,
				{Name: proto.String("tags"), Number: proto.Int32(2), Label: repLabel, Type: strType, JsonName: proto.String("tags")},
			}, &djinnv1.Unique{Fields: []string{"tags"}}),
			wantErr: true,
		},
		{
			name: "bytes unique field rejected",
			md: makeDesc(t, []*descriptorpb.FieldDescriptorProto{
				idField,
				{Name: proto.String("raw"), Number: proto.Int32(2), Label: optLabel, Type: bytesType, JsonName: proto.String("raw")},
			}, &djinnv1.Unique{Fields: []string{"raw"}}),
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tbl, err := newTable(tc.md)
			if (err != nil) != tc.wantErr {
				t.Fatalf("newTable = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && tbl.name == "" {
				t.Error("expected non-empty table name")
			}
		})
	}
}

func TestTable_create(t *testing.T) {
	t.Parallel()

	t.Run("field added without migration backfills indexed column", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), File)
		v1, v2 := thing(t, 1), thing(t, 2)
		set := func(m *dynamicpb.Message, field, value string) {
			m.Set(m.Descriptor().Fields().ByName(protoreflect.Name(field)), protoreflect.ValueOfString(value))
		}
		field := func(m *dynamicpb.Message, name string) string {
			return m.Get(m.Descriptor().Fields().ByName(protoreflect.Name(name))).String()
		}

		s, err := Open(ctx, path, dynamicpb.NewMessage(v1))
		if err != nil {
			t.Fatal(err)
		}
		old := dynamicpb.NewMessage(v1)
		oldID := NewID()
		set(old, "id", oldID)
		set(old, "name", "lamp")
		if err := put(ctx, s, old); err != nil {
			t.Fatal(err)
		}
		s.Close()

		// The new version adds two fields: the old row reads as is, and the new indexed column is filled.
		s, err = Open(ctx, path, dynamicpb.NewMessage(v2))
		if err != nil {
			t.Fatal(err)
		}
		if mode := strs(t, s, `PRAGMA journal_mode`); mode[0] != "wal" {
			t.Errorf("journal mode = %s, want wal", mode[0])
		}
		read := dynamicpb.NewMessage(v2)
		if err := get(ctx, s, oldID, read); err != nil || field(read, "name") != "lamp" || field(read, "note") != "" {
			t.Fatalf("old row read by v2 = %v, %v", read, err)
		}
		owner := NewID()
		fresh := dynamicpb.NewMessage(v2)
		set(fresh, "id", NewID())
		set(fresh, "name", "desk")
		set(fresh, "note", "by the window")
		set(fresh, "owner_id", owner)
		if err := put(ctx, s, fresh); err != nil {
			t.Fatal(err)
		}
		var names []string
		collect := func(m proto.Message) bool { names = append(names, field(m.(*dynamicpb.Message), "name")); return true }
		if err := list(ctx, s, dynamicpb.NewMessageType(v2), Where{"owner_id": owner}, "id", collect); err != nil || !slices.Equal(names, []string{"desk"}) {
			t.Errorf("List by the new indexed field = %v, %v", names, err)
		}
		s.Close()

		// An older binary still reads the new row, and keeps the field it does not know.
		s, err = Open(ctx, path, dynamicpb.NewMessage(v1))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		back := dynamicpb.NewMessage(v1)
		if err := get(ctx, s, field(fresh, "id"), back); err != nil || field(back, "name") != "desk" || len(back.GetUnknown()) == 0 {
			t.Errorf("new row read by v1 = %v, %v", back, err)
		}
	})
}

func TestTable_unique(t *testing.T) {
	t.Parallel()

	t.Run("ignores case and rolls back on duplicate", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		if err := put(ctx, s, &planv1.Project{Id: NewID(), Name: "Api", Directory: "/src/api"}); err != nil {
			t.Fatal(err)
		}
		err := put(ctx, s, &planv1.Project{Id: NewID(), Name: "API", Directory: "/src/other"})
		if !errors.Is(err, ErrDuplicate) {
			t.Fatalf("a name differing only by case: %v, want ErrDuplicate", err)
		}
		// The failed command left nothing: neither the entity nor its journal entry.
		if n := strs(t, s, `SELECT count(*) FROM project`); n[0] != "1" {
			t.Errorf("projects = %s, want 1", n[0])
		}
		if n := strs(t, s, `SELECT count(*) FROM command`); n[0] != "1" {
			t.Errorf("commands = %s, want 1", n[0])
		}
	})

	t.Run("composite group per wish", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		w1, w2 := NewID(), NewID()
		if err := put(ctx, s,
			&planv1.Question{Id: NewID(), WishId: w1, Code: "Q01"},
			&planv1.Question{Id: NewID(), WishId: w2, Code: "Q01"},
		); err != nil {
			t.Fatal(err)
		}
		if err := put(ctx, s, &planv1.Question{Id: NewID(), WishId: w1, Code: "q01"}); !errors.Is(err, ErrDuplicate) {
			t.Errorf("same code in the same wish: %v, want ErrDuplicate", err)
		}
	})

	t.Run("empty value does not collide and older index without WHERE is replaced", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := open(t)
		// Two projects without a directory do not collide.
		if err := put(ctx, s,
			&planv1.Project{Id: NewID(), Name: "one"},
			&planv1.Project{Id: NewID(), Name: "two"},
		); err != nil {
			t.Fatalf("projects with empty directory: %v", err)
		}
		// Simulate an older index without WHERE clause and verify migrate replaces it.
		if _, err := s.db.ExecContext(ctx, `DROP INDEX "project_unique_name"`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX "project_unique_name" ON "project" ("name")`); err != nil {
			t.Fatal(err)
		}
		if err := s.migrate(ctx, []proto.Message{&planv1.Project{}, &planv1.Question{}}); err != nil {
			t.Fatalf("migrate replacing older index: %v", err)
		}
		sqls := strs(t, s, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'project_unique_name'`)
		if len(sqls) != 1 || sqls[0] == `CREATE UNIQUE INDEX "project_unique_name" ON "project" ("name")` {
			t.Errorf("index was not replaced: %v", sqls)
		}
	})
}

func TestSnake(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "Project", want: "project"},
		{in: "TaskStatus", want: "task_status"},
		{in: "TaskEvent", want: "task_event"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := snake(tc.in); got != tc.want {
				t.Errorf("snake(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

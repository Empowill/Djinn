package store

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	testx.Main(m)
}

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), "", &planv1.Project{}, &planv1.Question{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// put writes entities in one transaction, journaled as one command.
func put(ctx context.Context, s *Store, ms ...proto.Message) error {
	return s.Tx(ctx, func(tx *Tx) error {
		if err := tx.Journal("test", "/test.v1.TestService/Put", &planv1.ProjectServiceListRequest{}); err != nil {
			return err
		}
		for _, m := range ms {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
}

func strs(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// thing builds version v of a test entity: v1 has id and name; v2 adds a note, kept in the payload only, and
// an owner_id, which becomes an indexed column.
func thing(t *testing.T, v int) protoreflect.MessageDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, n int32) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name: proto.String(name), Number: proto.Int32(n), Type: str, JsonName: proto.String(name),
		}
	}
	msg := &descriptorpb.DescriptorProto{
		Name:  proto.String("Thing"),
		Field: []*descriptorpb.FieldDescriptorProto{field("id", 1), field("name", 2)},
	}
	if v == 2 {
		msg.Field = append(msg.Field, field("note", 3), field("owner_id", 4))
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("thing.proto"), Package: proto.String("test.v1"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{msg},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Messages().Get(0)
}

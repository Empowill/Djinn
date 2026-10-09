package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

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

func TestSchema(t *testing.T) {
	s := open(t)
	tables := strs(t, s, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if want := []string{"command", "project", "question"}; !slices.Equal(tables, want) {
		t.Errorf("tables = %v, want %v", tables, want)
	}
	if cols, want := strs(t, s, `SELECT name FROM pragma_table_info('question')`), []string{"id", "payload", "wish_id", "task_id", "code"}; !slices.Equal(cols, want) {
		t.Errorf("question columns = %v, want %v", cols, want)
	}
	if cols, want := strs(t, s, `SELECT name FROM pragma_table_info('project')`), []string{"id", "payload", "name", "directory"}; !slices.Equal(cols, want) {
		t.Errorf("project columns = %v, want %v (git and create_time stay in the payload)", cols, want)
	}
	indexes := strs(t, s, `SELECT name FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL ORDER BY name`)
	want := []string{
		"project_unique_directory", "project_unique_name", "question_task_id", "question_unique_wish_id_code", "question_wish_id",
	}
	if !slices.Equal(indexes, want) {
		t.Errorf("indexes = %v, want %v", indexes, want)
	}
	if cols := strs(t, s, `SELECT name FROM pragma_table_info('command')`); !slices.Equal(cols, []string{"seq", "id", "actor", "at", "method", "request"}) {
		t.Errorf("command columns = %v", cols)
	}
}

func TestPutGetList(t *testing.T) {
	ctx := t.Context()
	s := open(t)
	api := &planv1.Project{Id: NewID(), Name: "Api", Directory: "/src/api", Git: true}
	web := &planv1.Project{Id: NewID(), Name: "web", Directory: "/src/web"}
	if err := put(ctx, s, api, web); err != nil {
		t.Fatal(err)
	}

	got, err := Get[*planv1.Project](ctx, s, api.GetId())
	if err != nil || !proto.Equal(got, api) {
		t.Fatalf("Get = %v, %v; want %v", got, err, api)
	}
	if _, err := Get[*planv1.Project](ctx, s, NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of an unknown id: %v, want ErrNotFound", err)
	}
	all, err := List[*planv1.Project](ctx, s, nil)
	if err != nil || len(all) != 2 || all[0].GetName() != "Api" || all[1].GetName() != "web" {
		t.Errorf("List = %v, %v; want api then web", all, err)
	}
	found, err := List[*planv1.Project](ctx, s, Where{"name": "API"})
	if err != nil || len(found) != 1 || found[0].GetId() != api.GetId() {
		t.Errorf("List by name ignoring case = %v, %v", found, err)
	}
	if _, err := List[*planv1.Project](ctx, s, Where{"git": true}); err == nil {
		t.Error("List by a field that is not indexed: no error")
	}

	// Put replaces by id.
	api.Name = "api-v2"
	if err := put(ctx, s, api); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get[*planv1.Project](ctx, s, api.GetId()); got.GetName() != "api-v2" {
		t.Errorf("after a second Put, name = %q", got.GetName())
	}
	if _, err := Get[*planv1.Wish](ctx, s, api.GetId()); err == nil {
		t.Error("Get of a type that is not an entity: no error")
	}
}

func TestUniqueIgnoresCase(t *testing.T) {
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
	// A composite group: the same code in two wishes is fine, twice in one wish is not.
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
}

func TestJournalAndTransaction(t *testing.T) {
	ctx := t.Context()
	s := open(t)
	p := &planv1.Project{Id: NewID(), Name: "api", Directory: "/src/api"}

	err := s.Tx(ctx, func(tx *Tx) error { return tx.Put(p) })
	if err == nil {
		t.Fatal("Put without Journal: no error")
	}

	boom := errors.New("boom")
	err = s.Tx(ctx, func(tx *Tx) error {
		if err := tx.Journal("lead", "/plan.v1.ProjectService/Add", &planv1.ProjectServiceAddRequest{Directory: "/src/api"}); err != nil {
			return err
		}
		if err := tx.Put(p); err != nil {
			return err
		}
		// The transaction reads what it wrote.
		if _, err := Get[*planv1.Project](ctx, tx, p.GetId()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v, want boom", err)
	}
	if n := strs(t, s, `SELECT count(*) FROM command`); n[0] != "0" {
		t.Errorf("a rolled back command is in the journal: %s rows", n[0])
	}
	if _, err := Get[*planv1.Project](ctx, s, p.GetId()); !errors.Is(err, ErrNotFound) {
		t.Errorf("a rolled back entity is stored: %v", err)
	}

	req := &planv1.ProjectServiceAddRequest{Directory: "/src/api", Name: "api"}
	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.Journal("lead", "/plan.v1.ProjectService/Add", req); err != nil {
			return err
		}
		return tx.Put(p)
	}); err != nil {
		t.Fatal(err)
	}
	var seq int64
	var id, actor, at, method string
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT seq, id, actor, at, method, request FROM command`).
		Scan(&seq, &id, &actor, &at, &method, &raw); err != nil {
		t.Fatal(err)
	}
	got := &planv1.ProjectServiceAddRequest{}
	if err := proto.Unmarshal(raw, got); err != nil || !proto.Equal(got, req) {
		t.Errorf("journaled request = %v, %v; want %v", got, err, req)
	}
	if seq != 1 || len(id) != 36 || actor != "lead" || at == "" || method != "/plan.v1.ProjectService/Add" {
		t.Errorf("command = %d %q %q %q %q", seq, id, actor, at, method)
	}
}

// thing builds version v of a test entity: v1 has id and name; v2 adds a note, kept in the payload only, and
// an owner_id, which becomes an indexed column.
func thing(t *testing.T, v int) protoreflect.MessageDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, n int32) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(n), Type: str, JsonName: proto.String(name)}
	}
	msg := &descriptorpb.DescriptorProto{Name: proto.String("Thing"), Field: []*descriptorpb.FieldDescriptorProto{field("id", 1), field("name", 2)}}
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

func TestFieldAddedWithoutMigration(t *testing.T) {
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
	collect := func(m proto.Message) { names = append(names, field(m.(*dynamicpb.Message), "name")) }
	if err := list(ctx, s, dynamicpb.NewMessageType(v2), Where{"owner_id": owner}, collect); err != nil || !slices.Equal(names, []string{"desk"}) {
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
}

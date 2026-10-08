package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// table maps one message to its table: id, the indexed fields as columns, and the payload.
type table struct {
	md      protoreflect.MessageDescriptor
	name    string
	id      protoreflect.FieldDescriptor
	columns []protoreflect.FieldDescriptor // indexed fields, id aside
	uniques [][]string
}

func newTable(md protoreflect.MessageDescriptor) (*table, error) {
	t := &table{md: md, name: snake(string(md.Name())), id: md.Fields().ByName("id")}
	if t.id == nil || t.id.Kind() != protoreflect.StringKind || t.id.IsList() {
		return nil, fmt.Errorf("entity %s needs a string id field", md.FullName())
	}
	add := func(fd protoreflect.FieldDescriptor) error {
		if fd == t.id || slices.Contains(t.columns, fd) {
			return nil
		}
		if _, err := sqlType(fd); err != nil {
			return fmt.Errorf("entity %s: field %s: %w", md.FullName(), fd.Name(), err)
		}
		t.columns = append(t.columns, fd)
		return nil
	}
	for i := range md.Fields().Len() {
		fd := md.Fields().Get(i)
		// A repeated *_ids field is not indexed: it would need a table of its own, and no query needs it yet.
		if strings.HasSuffix(string(fd.Name()), "_id") && fd.Kind() == protoreflect.StringKind && !fd.IsList() {
			if err := add(fd); err != nil {
				return nil, err
			}
		}
	}
	groups, _ := proto.GetExtension(md.Options(), djinnv1.E_Unique).([]*djinnv1.Unique)
	for _, g := range groups {
		if len(g.GetFields()) == 0 {
			return nil, fmt.Errorf("entity %s: an empty unique group", md.FullName())
		}
		for _, name := range g.GetFields() {
			fd := md.Fields().ByName(protoreflect.Name(name))
			if fd == nil {
				return nil, fmt.Errorf("entity %s: unique field %s does not exist", md.FullName(), name)
			}
			if err := add(fd); err != nil {
				return nil, err
			}
		}
		t.uniques = append(t.uniques, g.GetFields())
	}
	return t, nil
}

// sqlType is the column type of an indexed field: text ignores case, like the names Djinn compares.
func sqlType(fd protoreflect.FieldDescriptor) (string, error) {
	if fd.IsList() || fd.IsMap() {
		return "", errors.New("a repeated field cannot be indexed")
	}
	switch fd.Kind() {
	case protoreflect.StringKind:
		return "TEXT NOT NULL DEFAULT '' COLLATE NOCASE", nil
	case protoreflect.BoolKind, protoreflect.EnumKind,
		protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "INTEGER NOT NULL DEFAULT 0", nil
	}
	return "", fmt.Errorf("a %s field cannot be indexed", fd.Kind())
}

func (t *table) column(name string) protoreflect.FieldDescriptor {
	if name == "id" {
		return t.id
	}
	for _, fd := range t.columns {
		if string(fd.Name()) == name {
			return fd
		}
	}
	return nil
}

// create creates the table and its indexes, and adds the columns it lacks. Rows written before a column existed
// get its value from their payload. Nothing is dropped.
func (t *table) create(ctx context.Context, tx *sql.Tx, mt protoreflect.MessageType) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+quote(t.name)+
		` (id TEXT PRIMARY KEY COLLATE NOCASE, payload BLOB NOT NULL)`); err != nil {
		return err
	}
	have, err := t.existing(ctx, tx)
	if err != nil {
		return err
	}
	added := false
	for _, fd := range t.columns {
		if have[string(fd.Name())] {
			continue
		}
		typ, _ := sqlType(fd)
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+quote(t.name)+` ADD COLUMN `+quote(string(fd.Name()))+` `+typ); err != nil {
			return err
		}
		added = true
	}
	if added {
		if err := t.backfill(ctx, tx, mt); err != nil {
			return err
		}
	}
	for _, fd := range t.columns {
		if !strings.HasSuffix(string(fd.Name()), "_id") {
			continue
		}
		name := t.name + "_" + string(fd.Name())
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+quote(name)+` ON `+quote(t.name)+
			` (`+quote(string(fd.Name()))+`)`); err != nil {
			return err
		}
	}
	for _, fields := range t.uniques {
		if err := t.unique(ctx, tx, fields); err != nil {
			return fmt.Errorf("unique %s: %w", strings.Join(fields, ", "), err)
		}
	}
	return nil
}

// unique creates the unique index of a group of fields. A row where a field of the group holds its zero value is
// left out: an empty value names nothing, so two projects without a folder do not collide. An index written by an
// older Djinn, without that clause, is replaced.
func (t *table) unique(ctx context.Context, tx *sql.Tx, fields []string) error {
	quoted := make([]string, len(fields))
	set := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = quote(f)
		zero := "''"
		if t.column(f).Kind() != protoreflect.StringKind {
			zero = "0"
		}
		set[i] = quote(f) + " <> " + zero
	}
	name := t.name + "_unique_" + strings.Join(fields, "_")
	want := `CREATE UNIQUE INDEX ` + quote(name) + ` ON ` + quote(t.name) + ` (` + strings.Join(quoted, ", ") +
		`) WHERE ` + strings.Join(set, " AND ")
	var have string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&have)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case have == want:
		return nil
	default:
		if _, err := tx.ExecContext(ctx, `DROP INDEX `+quote(name)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, want)
	return err
}

// remove deletes the row with this id; deleting a row that does not exist is not an error.
func (t *table) remove(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM `+quote(t.name)+` WHERE id = ?`, id)
	return err
}

func (t *table) existing(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, t.name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		have[name] = true
	}
	return have, rows.Err()
}

// backfill writes again every row from its payload, which fills the columns just added.
func (t *table) backfill(ctx context.Context, tx *sql.Tx, mt protoreflect.MessageType) error {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM `+quote(t.name))
	if err != nil {
		return err
	}
	var all []proto.Message
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return err
		}
		m := mt.New().Interface()
		if err := proto.Unmarshal(payload, m); err != nil {
			rows.Close()
			return err
		}
		all = append(all, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range all {
		if err := t.put(ctx, tx, m); err != nil {
			return err
		}
	}
	return nil
}

// put inserts or replaces m, its columns taken from its fields.
func (t *table) put(ctx context.Context, tx *sql.Tx, m proto.Message) error {
	r := m.ProtoReflect()
	id := r.Get(t.id).String()
	if id == "" {
		return fmt.Errorf("%s without an id", t.md.Name())
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return err
	}
	names := []string{"id"}
	args := []any{id}
	for _, fd := range t.columns {
		names = append(names, string(fd.Name()))
		args = append(args, value(fd, r.Get(fd)))
	}
	names = append(names, "payload")
	args = append(args, payload)
	quoted := make([]string, len(names))
	set := make([]string, 0, len(names)-1)
	for i, n := range names {
		quoted[i] = quote(n)
		if n != "id" {
			set = append(set, quote(n)+" = excluded."+quote(n))
		}
	}
	q := `INSERT INTO ` + quote(t.name) + ` (` + strings.Join(quoted, ", ") + `) VALUES (?` +
		strings.Repeat(", ?", len(names)-1) + `) ON CONFLICT (id) DO UPDATE SET ` + strings.Join(set, ", ")
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		if isUnique(err) {
			return fmt.Errorf("%s %s: %w: %v", t.md.Name(), id, ErrDuplicate, err)
		}
		return err
	}
	return nil
}

// value is the column value of a field.
func value(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.BoolKind:
		if v.Bool() {
			return 1
		}
		return 0
	case protoreflect.EnumKind:
		return int64(v.Enum())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return int64(v.Uint())
	}
	return v.Int()
}

// snake turns a message name into a table name: TaskStatus is task_status.
func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

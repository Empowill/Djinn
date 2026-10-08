// Package store keeps the entities of Djinn in one SQLite database, by protobuf reflection: one table per message,
// its id as primary key, the fields it is searched by as columns, and the whole message as a binary payload. A
// field that is not searched by lives only in the payload, so adding one needs no migration.
//
// Every change goes through Tx, which writes the command that caused it to the journal in the same transaction.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// File is the database of the machine, in the data directory.
const File = "djinn.db"

var (
	// ErrNotFound is returned by Get when no entity has the id.
	ErrNotFound = errors.New("not found")
	// ErrDuplicate is returned by Put when another entity has the same values in a unique group of fields.
	ErrDuplicate = errors.New("duplicate")
)

// Store is the database. It is safe for concurrent use: transactions run one at a time.
type Store struct {
	db     *sql.DB
	tables map[protoreflect.FullName]*table

	hooksMu sync.RWMutex
	hooks   []func([]proto.Message)
}

// OnCommit calls f after each committed transaction that changed entities, with what it put and deleted, in
// order; a deleted entity may hold its id only. f runs in the writer's goroutine, after the commit: it must be
// quick, and must neither keep nor change the messages.
func (s *Store) OnCommit(f func([]proto.Message)) {
	s.hooksMu.Lock()
	defer s.hooksMu.Unlock()
	s.hooks = append(s.hooks, f)
}

// Open opens the database file at path, creating it and its directory if needed, in WAL mode. An empty path
// opens a database in memory, for tests. The tables of entities are created, and the columns they lack added;
// nothing is ever dropped.
//
// Each entity is a message with a string id field. Its singular string fields named *_id are indexed, and each
// (djinn.v1.unique) option becomes a unique index, ignoring case on text.
func Open(ctx context.Context, path string, entities ...proto.Message) (*Store, error) {
	dsn := ":memory:"
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// synchronous(FULL): a committed write survives a power cut, not only a crash of Djinn. Djinn writes
		// little, so one fsync per transaction costs nothing worth saving.
		q := url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "synchronous(FULL)"}}
		dsn = "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: an in-memory database lives in it, and writes are serialized without SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, tables: map[protoreflect.FullName]*table{}}
	if err := s.migrate(ctx, entities); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context, entities []proto.Message) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS command (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			actor TEXT NOT NULL,
			at TEXT NOT NULL,
			method TEXT NOT NULL,
			request BLOB NOT NULL
		)`); err != nil {
			return fmt.Errorf("create the journal: %w", err)
		}
		names := map[string]bool{"command": true}
		for _, e := range entities {
			t, err := newTable(e.ProtoReflect().Descriptor())
			if err != nil {
				return err
			}
			if names[t.name] {
				return fmt.Errorf("entity %s: table %s is taken", t.md.FullName(), t.name)
			}
			names[t.name] = true
			if err := t.create(ctx, tx, e.ProtoReflect().Type()); err != nil {
				return fmt.Errorf("entity %s: %w", t.md.FullName(), err)
			}
			s.tables[t.md.FullName()] = t
		}
		return nil
	})
}

// Reader is the store or a transaction: what Get and List read from.
type Reader interface {
	query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	table(md protoreflect.MessageDescriptor) (*table, error)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

func (s *Store) table(md protoreflect.MessageDescriptor) (*table, error) {
	t, ok := s.tables[md.FullName()]
	if !ok {
		return nil, fmt.Errorf("%s is not an entity of the store", md.FullName())
	}
	return t, nil
}

// Where filters List: each key is an indexed field, compared for equality, ignoring case on text.
type Where map[string]any

// Get returns the entity of type T with this id, or an error wrapping ErrNotFound.
func Get[T proto.Message](ctx context.Context, r Reader, id string) (T, error) {
	var zero T
	m := zero.ProtoReflect().Type().New().Interface().(T)
	if err := get(ctx, r, id, m); err != nil {
		return zero, err
	}
	return m, nil
}

// List returns the entities of type T that match where, oldest first: ids are UUIDv7, ordered by time.
func List[T proto.Message](ctx context.Context, r Reader, where Where) ([]T, error) {
	var zero T
	mt := zero.ProtoReflect().Type()
	var out []T
	err := list(ctx, r, mt, where, func(m proto.Message) { out = append(out, m.(T)) })
	return out, err
}

func get(ctx context.Context, r Reader, id string, m proto.Message) error {
	md := m.ProtoReflect().Descriptor()
	t, err := r.table(md)
	if err != nil {
		return err
	}
	rows, err := r.query(ctx, `SELECT payload FROM `+quote(t.name)+` WHERE id = ?`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("%s %s: %w", md.Name(), id, ErrNotFound)
	}
	var payload []byte
	if err := rows.Scan(&payload); err != nil {
		return err
	}
	return proto.Unmarshal(payload, m)
}

func list(ctx context.Context, r Reader, mt protoreflect.MessageType, where Where, add func(proto.Message)) error {
	t, err := r.table(mt.Descriptor())
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(where))
	for k := range where {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	conds := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		if t.column(k) == nil {
			return fmt.Errorf("%s: %s is not an indexed field", mt.Descriptor().Name(), k)
		}
		conds = append(conds, quote(k)+" = ?")
		args = append(args, where[k])
	}
	q := `SELECT payload FROM ` + quote(t.name)
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := r.query(ctx, q+" ORDER BY id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		m := mt.New().Interface()
		if err := proto.Unmarshal(payload, m); err != nil {
			return err
		}
		add(m)
	}
	return rows.Err()
}

// Tx is a transaction: it reads what it wrote, and writes the journal and the entities together.
type Tx struct {
	ctx     context.Context
	tx      *sql.Tx
	s       *Store
	journal bool
	changed []proto.Message
}

// Tx runs fn in a transaction, committed when fn returns nil and rolled back otherwise. A change must follow the
// command that caused it: call Journal before Put.
func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	t := &Tx{ctx: ctx, s: s}
	if err := s.tx(ctx, func(tx *sql.Tx) error { t.tx = tx; return fn(t) }); err != nil {
		return err
	}
	if len(t.changed) > 0 {
		s.hooksMu.RLock()
		hooks := s.hooks
		s.hooksMu.RUnlock()
		for _, f := range hooks {
			f(t.changed)
		}
	}
	return nil
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func (tx *Tx) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, query, args...)
}

func (tx *Tx) table(md protoreflect.MessageDescriptor) (*table, error) { return tx.s.table(md) }

// Journal writes the command received: who sent it, the full name of its method, and the request as is.
func (tx *Tx) Journal(actor, method string, req proto.Message) error {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.tx.ExecContext(tx.ctx, `INSERT INTO command (id, actor, at, method, request) VALUES (?, ?, ?, ?, ?)`,
		id.String(), actor, at, method, b); err != nil {
		return fmt.Errorf("journal %s: %w", method, err)
	}
	tx.journal = true
	return nil
}

// Put inserts or replaces an entity. It returns an error wrapping ErrDuplicate when another entity has the same
// values in a unique group.
func (tx *Tx) Put(m proto.Message) error {
	if !tx.journal {
		return errors.New("store: journal the command before changing an entity")
	}
	t, err := tx.s.table(m.ProtoReflect().Descriptor())
	if err != nil {
		return err
	}
	if err := t.put(tx.ctx, tx.tx, m); err != nil {
		return err
	}
	tx.changed = append(tx.changed, m)
	return nil
}

// Delete removes an entity, found by the type and the id of m. Like Put, it follows the command that caused it.
func (tx *Tx) Delete(m proto.Message) error {
	if !tx.journal {
		return errors.New("store: journal the command before changing an entity")
	}
	t, err := tx.s.table(m.ProtoReflect().Descriptor())
	if err != nil {
		return err
	}
	if err := t.remove(tx.ctx, tx.tx, m.ProtoReflect().Get(t.id).String()); err != nil {
		return err
	}
	tx.changed = append(tx.changed, m)
	return nil
}

// Command is an entry of the journal.
type Command struct {
	Seq     int64
	ID      string
	Actor   string
	At      time.Time
	Method  string
	Request []byte
}

// Commands returns the entries of the journal, oldest first, for which keep returns true. keep sees each entry
// once, in order; it may be nil to keep them all.
func Commands(ctx context.Context, r Reader, keep func(Command) bool) ([]Command, error) {
	rows, err := r.query(ctx, `SELECT seq, id, actor, at, method, request FROM command ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		var c Command
		var at string
		if err := rows.Scan(&c.Seq, &c.ID, &c.Actor, &at, &c.Method, &c.Request); err != nil {
			return nil, err
		}
		if c.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("journal entry %d: %w", c.Seq, err)
		}
		if keep == nil || keep(c) {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// NewID returns a new UUIDv7, the identifier of every entity.
func NewID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// isUnique tells whether err is the violation of a unique index.
func isUnique(err error) bool {
	var serr *sqlite.Error
	return errors.As(err, &serr) && serr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// quote quotes an identifier for SQL. Names come from proto descriptors: letters, digits and underscores.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

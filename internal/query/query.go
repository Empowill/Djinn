package query

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/empowill/djinn/internal/store"
)

// Source gives the messages of a collection.
type Source interface {
	// Descriptor is the message the source gives.
	Descriptor() protoreflect.MessageDescriptor
	// Indexed names the singular text fields where narrows by.
	Indexed() []string
	// Load gives the messages whose fields equal where's values, text ignoring case: all of them for an empty where.
	Load(ctx context.Context, where map[string]string) ([]proto.Message, error)
}

// FromStore is the source of the entities of m's type in the store: where narrows by their text columns, the *_id
// fields and the unique ones, in SQL.
func FromStore(r store.Reader, m proto.Message) (Source, error) {
	mt := m.ProtoReflect().Type()
	columns, err := store.Indexed(r, mt.Descriptor())
	if err != nil {
		return nil, err
	}
	var text []string
	for _, name := range columns {
		if fd := mt.Descriptor().Fields().ByName(protoreflect.Name(name)); fd != nil && fd.Kind() == protoreflect.StringKind {
			text = append(text, name)
		}
	}
	return &storeSource{r: r, mt: mt, indexed: text}, nil
}

type storeSource struct {
	r       store.Reader
	mt      protoreflect.MessageType
	indexed []string
}

func (s *storeSource) Descriptor() protoreflect.MessageDescriptor { return s.mt.Descriptor() }
func (s *storeSource) Indexed() []string                          { return s.indexed }

func (s *storeSource) Load(ctx context.Context, where map[string]string) ([]proto.Message, error) {
	w := make(store.Where, len(where))
	for k, v := range where {
		w[k] = v
	}
	var out []proto.Message
	err := store.Each(ctx, s.r, s.mt, w, func(m proto.Message) bool { out = append(out, m); return true })
	return out, err
}

// Collection is a source a query names.
type Collection struct {
	// Name is how a query names it, such as TASKS.
	Name   string
	Source Source
	// Defaults are the refs a query without selects reads: every ref of the schema when empty.
	Defaults []string
	// Derived are the refs it computes in Go, besides its fields' (TaskRefs, QuestionRefs).
	Derived []Derived
}

// Catalog holds the collections a query reads, one per message type: a jump reads its target by its type.
type Catalog struct {
	byName  map[string]Collection
	byType  map[protoreflect.FullName]Source
	derived map[protoreflect.FullName]map[string]Derived
}

// NewCatalog checks the collections: a name and a type each once, and default refs that compile.
func NewCatalog(collections ...Collection) (*Catalog, error) {
	c := &Catalog{byName: map[string]Collection{}, byType: map[protoreflect.FullName]Source{},
		derived: map[protoreflect.FullName]map[string]Derived{}}
	for _, col := range collections {
		if col.Name == "" || col.Source == nil {
			return nil, fmt.Errorf("a collection needs a name and a source")
		}
		if _, ok := c.byName[col.Name]; ok {
			return nil, fmt.Errorf("collection %s twice", col.Name)
		}
		name := col.Source.Descriptor().FullName()
		if _, ok := c.byType[name]; ok {
			return nil, fmt.Errorf("collection %s: %s is another collection's already", col.Name, name)
		}
		c.byName[col.Name] = col
		c.byType[name] = col.Source
		for _, d := range col.Derived {
			if err := d.check(col.Source.Descriptor()); err != nil {
				return nil, fmt.Errorf("collection %s: %w", col.Name, err)
			}
			if _, ok := c.derived[name][d.Ref]; ok {
				return nil, fmt.Errorf("collection %s: derived ref %q twice", col.Name, d.Ref)
			}
			if c.derived[name] == nil {
				c.derived[name] = map[string]Derived{}
			}
			c.derived[name][d.Ref] = d
		}
	}
	for _, col := range collections {
		comp := &compiler{cat: c, targets: map[protoreflect.FullName]map[string]protoreflect.Message{}}
		for _, ref := range col.Defaults {
			if _, err := comp.ref(col.Source.Descriptor(), ref); err != nil {
				return nil, fmt.Errorf("collection %s: default %w", col.Name, err)
			}
		}
	}
	return c, nil
}

// Schema gives the refs of a collection: its own, without jumps, then its derived ones.
func (c *Catalog) Schema(name string) ([]Column, error) {
	col, ok := c.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: no collection %s", ErrInvalid, name)
	}
	return c.schema(col), nil
}

func (c *Catalog) schema(col Collection) []Column {
	out := Schema(col.Source.Descriptor())
	for _, d := range col.Derived {
		out = append(out, d.column())
	}
	return out
}

// Query asks a collection for a table.
type Query struct {
	Collection string
	// Filters all hold for a row the table keeps.
	Filters []Filter
	// Sorts order the rows, the first one first; then rows keep the source's order, oldest first. A null comes
	// last either way.
	Sorts []Sort
	// Selects are the refs of the table's columns, in order: the collection's defaults when empty.
	Selects []string
	// Limit keeps the first rows only, when above 0.
	Limit int
	// Aggregation, when set, makes the table a row per group, its columns the groups' and the measures': it
	// selects them, and the sorts name them.
	Aggregation *Aggregation
}

// Sort orders rows by a ref, which cannot be a list.
type Sort struct {
	Ref  string
	Desc bool
}

// Table is a query's answer.
type Table struct {
	Columns []Column
	// Rows hold a value per column, nil when absent.
	Rows [][]any
	// Total counts the rows before the limit: the groups when aggregated.
	Total int
}

// Run runs q. Its errors wrap ErrInvalid when q cannot run as written.
func (c *Catalog) Run(ctx context.Context, q Query) (*Table, error) {
	col, ok := c.byName[q.Collection]
	if !ok {
		return nil, fmt.Errorf("%w: no collection %s", ErrInvalid, q.Collection)
	}
	if q.Limit < 0 {
		return nil, fmt.Errorf("%w: a limit below 0", ErrInvalid)
	}
	md := col.Source.Descriptor()
	comp := &compiler{cat: c, targets: map[protoreflect.FullName]map[string]protoreflect.Message{}}

	var (
		cols  []compiled
		ag    *aggregator
		sorts []compiled
		desc  []bool
	)
	for _, s := range q.Sorts {
		desc = append(desc, s.Desc)
	}
	if q.Aggregation != nil {
		if len(q.Selects) > 0 {
			return nil, fmt.Errorf("%w: an aggregation selects its groups and measures", ErrInvalid)
		}
		var err error
		if ag, err = comp.aggregation(md, *q.Aggregation); err != nil {
			return nil, err
		}
	} else {
		selects := q.Selects
		if len(selects) == 0 {
			selects = col.Defaults
		}
		if len(selects) == 0 {
			for _, s := range c.schema(col) {
				selects = append(selects, s.Ref)
			}
		}
		for _, ref := range selects {
			if slices.ContainsFunc(cols, func(r compiled) bool { return r.col.Ref == ref }) {
				return nil, fmt.Errorf("%w: ref %q selected twice", ErrInvalid, ref)
			}
			r, err := comp.ref(md, ref)
			if err != nil {
				return nil, err
			}
			cols = append(cols, r)
		}
		for _, s := range q.Sorts {
			r, err := comp.ref(md, s.Ref)
			if err != nil {
				return nil, err
			}
			if r.col.List {
				return nil, fmt.Errorf("%w: ref %q: a list does not sort", ErrInvalid, s.Ref)
			}
			sorts = append(sorts, r)
		}
	}
	var preds []pred
	for _, f := range q.Filters {
		if f == nil {
			return nil, fmt.Errorf("%w: a nil filter", ErrInvalid)
		}
		p, err := f.compile(comp, md)
		if err != nil {
			return nil, err
		}
		preds = append(preds, p)
	}
	// An aggregated table sorts by its columns: a group's or a measure's name.
	var sortAt []int
	if ag != nil {
		for _, s := range q.Sorts {
			i := slices.IndexFunc(ag.columns, func(c Column) bool { return c.Ref == s.Ref })
			if i < 0 {
				return nil, fmt.Errorf("%w: sort %q: the aggregation has no such column", ErrInvalid, s.Ref)
			}
			if ag.columns[i].List {
				return nil, fmt.Errorf("%w: ref %q: a list does not sort", ErrInvalid, s.Ref)
			}
			sortAt = append(sortAt, i)
		}
	}

	for name := range comp.targets {
		msgs, err := c.byType[name].Load(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", name, err)
		}
		byID := make(map[string]protoreflect.Message, len(msgs))
		for _, m := range msgs {
			pm := m.ProtoReflect()
			if fd := pm.Descriptor().Fields().ByName("id"); fd != nil {
				byID[strings.ToLower(pm.Get(fd).String())] = pm
			}
		}
		comp.targets[name] = byID
	}
	msgs, err := col.Source.Load(ctx, pushdown(col.Source, q.Filters))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", q.Collection, err)
	}
	kept := make([]protoreflect.Message, 0, len(msgs))
next:
	for _, msg := range msgs {
		m := msg.ProtoReflect()
		for _, p := range preds {
			if !p(m) {
				continue next
			}
		}
		kept = append(kept, m)
	}

	if ag != nil {
		rows := ag.run(kept)
		if len(sortAt) > 0 {
			sortCols := make([]Column, len(sortAt))
			for i, at := range sortAt {
				sortCols[i] = ag.columns[at]
			}
			keys := func(row []any) []any {
				out := make([]any, len(sortAt))
				for i, at := range sortAt {
					out[i] = row[at]
				}
				return out
			}
			slices.SortStableFunc(rows, func(a, b []any) int { return order(sortCols, desc, keys(a), keys(b)) })
		}
		t := &Table{Columns: ag.columns, Total: len(rows)}
		if q.Limit > 0 {
			rows = rows[:min(q.Limit, len(rows))]
		}
		t.Rows = rows
		return t, nil
	}

	type row struct {
		m    protoreflect.Message
		keys []any
	}
	rows := make([]row, 0, len(kept))
	for _, m := range kept {
		r := row{m: m}
		for _, s := range sorts {
			r.keys = append(r.keys, s.get(m))
		}
		rows = append(rows, r)
	}
	sortCols := make([]Column, len(sorts))
	for i, s := range sorts {
		sortCols[i] = s.col
	}
	slices.SortStableFunc(rows, func(a, b row) int { return order(sortCols, desc, a.keys, b.keys) })

	t := &Table{Total: len(rows)}
	if q.Limit > 0 {
		rows = rows[:min(q.Limit, len(rows))]
	}
	for _, r := range cols {
		t.Columns = append(t.Columns, r.col)
	}
	t.Rows = make([][]any, 0, len(rows))
	for _, r := range rows {
		values := make([]any, len(cols))
		for i, c := range cols {
			values[i] = c.get(r.m)
		}
		t.Rows = append(t.Rows, values)
	}
	return t, nil
}

// order compares two rows by their keys, one per column, descending where desc says (none when nil): a null comes
// last either way.
func order(cols []Column, desc []bool, a, b []any) int {
	for i := range min(len(a), len(cols)) {
		x, y := a[i], b[i]
		switch {
		case x == nil && y == nil:
			continue
		case x == nil:
			return 1
		case y == nil:
			return -1
		}
		c := compare(cols[i], x, y)
		if desc != nil && desc[i] {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// pushdown gives the source the equalities on its indexed fields among the filters at the root, which all hold for
// a row: the rows it gives are fewer, and every filter still checks them.
func pushdown(src Source, filters []Filter) map[string]string {
	where := map[string]string{}
	for _, f := range filters {
		if t, ok := f.(typed); ok {
			f = t.Filter // A typed comparison of QueryService narrows as its filter does.
		}
		e, ok := f.(Equal)
		if !ok {
			continue
		}
		v, ok := e.Value.(string)
		if !ok || !slices.Contains(src.Indexed(), e.Ref) {
			continue
		}
		if have, ok := where[e.Ref]; ok && !strings.EqualFold(have, v) {
			continue // Two values for one field: no row holds both, which the filters find.
		}
		where[e.Ref] = v
	}
	return where
}

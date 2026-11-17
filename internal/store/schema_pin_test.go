package store_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// pinned holds the digest of the tables and columns of each schema, by its number: the one past the last is
// store.Schema. A change that adds a column or a table appends its digest and increments store.Schema; it never
// edits a digest already here, which a released Djinn knows by its number.
var pinned = []string{
	1: "e517e32093ba9b9f92ecbdd67fd42d8b566276618b694873285c80c301decd66",
}

// TestSchemaPinned fails when the tables of Djinn's database change without store.Schema: an older Djinn opening the
// newer database would not know it, nor warn that it leaves the new columns stale (V5 of L08, W52, Q42).
func TestSchemaPinned(t *testing.T) {
	t.Parallel()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.DB().QueryContext(t.Context(), `SELECT m.name, c.name FROM sqlite_master m, pragma_table_info(m.name) c
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, c.cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s.%s\n", table, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
	if len(pinned)-1 != store.Schema {
		t.Fatalf("store.Schema is %d, pinned lists %d schemas: append one digest per schema", store.Schema, len(pinned)-1)
	}
	if want := pinned[store.Schema]; got != want {
		t.Errorf("the tables changed (digest %s, pinned %s): increment store.Schema and append %q to pinned; "+
			"the tables now:\n%s", got, want, got, b.String())
	}
	seen := map[string]int{}
	for n, d := range pinned[1:] {
		if prev, ok := seen[d]; ok {
			t.Errorf("schemas %d and %d have the same digest", prev+1, n+1)
		}
		seen[d] = n
	}
}

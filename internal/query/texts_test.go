package query

import (
	"context"
	"slices"
	"testing"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
	"github.com/empowill/djinn/internal/querytext"
)

// textFilter is the filter s says on the tasks of c, read as the command line reads it, its refs typed by c's
// schema, then as QueryService reads the message.
func textFilter(t *testing.T, c *Catalog, s string) Filter {
	t.Helper()
	types := querytext.NewTypes(func(_ context.Context, col queryv1.Collection) ([]*queryv1.QueryColumn, error) {
		cols, err := c.Schema(collection(col))
		if err != nil {
			return nil, err
		}
		out := make([]*queryv1.QueryColumn, len(cols))
		for i, col := range cols {
			out[i] = c.queryColumn(col)
		}
		return out, nil
	})
	msg, err := querytext.Filter(t.Context(), s, queryv1.Collection_COLLECTION_TASKS, types)
	if err != nil {
		t.Fatal(err)
	}
	f, err := filterOf(msg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestTextFilters runs the filters' texts, read as the command line reads them, on the frozen set: what each keeps,
// and the equality the store narrows by.
func TestTextFilters(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		filter string
		want   []string
	}{
		{"status=running", []string{"W2"}},
		{"status=RUNNING|failed", []string{"W2", "W3"}},
		{"status=null", []string{"W1"}},
		{"status=done|null", []string{"W1", "W1"}},
		{"status!=done", []string{"W2", "W1", "W3"}},
		{"status!=null", []string{"W1", "W2", "W3"}},
		{"usage.cost_usd>0", []string{"W1", "W3"}},
		{"usage.cost_usd<=0.25", []string{"W2", "W3"}},
		{"create_time>=2026-11-03T09:00:00Z", []string{"W2", "W1"}},
		{"running>=1h", []string{"W1"}},
		{"wish_id->title=stats", []string{"W1", "W2"}},
		{"title*=QUERY", []string{"W1"}},
		{"title^=a", []string{"W2", "W3"}},
		{"code=w1", []string{"W1", "W1"}},
	} {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()
			c, _ := catalog(t)
			f := textFilter(t, c, tt.filter)
			table, err := c.Run(t.Context(), Query{Collection: "TASKS", Selects: []string{"code"}, Filters: []Filter{f}})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, row := range table.Rows {
				got = append(got, row[0].(string))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("codes = %v; want %v", got, tt.want)
			}
		})
	}

	t.Run("an equality on an indexed field narrows the load", func(t *testing.T) {
		t.Parallel()
		c, tasks := catalog(t)
		f := textFilter(t, c, "wish_id="+id(3))
		if _, err := c.Run(t.Context(), Query{Collection: "TASKS", Filters: []Filter{f}}); err != nil {
			t.Fatal(err)
		}
		if len(tasks.wheres) != 1 || tasks.wheres[0]["wish_id"] != id(3) {
			t.Errorf("wheres = %v; want wish_id narrowed", tasks.wheres)
		}
	})
}

package store

import (
	"strings"
	"testing"
	"time"
)

// TestIntervals: gate intervals are read back after from, the first released first, and pruned by their release.
func TestIntervals(t *testing.T) {
	t.Parallel()
	s := open(t)
	ctx := t.Context()
	now := time.UnixMilli(time.Now().UnixMilli())
	for i, ago := range []time.Duration{8 * 24 * time.Hour, 2 * time.Hour, time.Minute} {
		if err := s.AddInterval(ctx, Interval{Released: now.Add(-ago), Data: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Intervals(ctx, now.Add(-3*time.Hour))
	if err != nil || len(got) != 2 || got[0].Data[0] != 1 || got[1].Data[0] != 2 || !got[1].Released.Equal(now.Add(-time.Minute)) {
		t.Fatalf("intervals of the last 3 hours = %v, %v", got, err)
	}
	if err := s.PruneIntervals(ctx, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Intervals(ctx, time.Time{}); err != nil || len(got) != 2 {
		t.Errorf("after the prune = %v, %v", got, err)
	}
}

// fillIntervals writes n gate intervals released evenly over the span before now, in one transaction: the rows of
// three months of gates (Q49).
func fillIntervals(tb testing.TB, s *Store, now time.Time, span time.Duration, n int) {
	tb.Helper()
	tx, err := s.db.BeginTx(tb.Context(), nil)
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after the commit
	data := make([]byte, 160)
	step := span / time.Duration(n)
	for i := range n {
		if _, err := tx.Exec(`INSERT INTO gate_interval (released, data) VALUES (?, ?)`,
			now.Add(-time.Duration(i)*step).UnixMilli(), data); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

// TestIntervalsReadByIndex: a window of gate intervals and their prune go by the index on the release time, so that
// a read of an hour or a day costs its own rows, not the three months kept.
func TestIntervalsReadByIndex(t *testing.T) {
	t.Parallel()
	s := open(t)
	for _, q := range []string{
		`SELECT released, data FROM gate_interval WHERE released > ? ORDER BY released`,
		`DELETE FROM gate_interval WHERE released < ?`,
	} {
		if p := queryPlan(t, s, q); !strings.Contains(p, "USING INDEX gate_interval_released") ||
			strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("plan of %q = %q, want a search by gate_interval_released", q, p)
		}
	}
}

// queryPlan gives SQLite's plan of q, one argument bound, its steps joined by "; ".
func queryPlan(t *testing.T, s *Store, q string) string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+q, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "; ")
}

// BenchmarkIntervals reads the windows of the Gates tab (an hour, 6 hours, a day) and of the series (a week) among
// 160,000 gate intervals over 90 days, the rows of three months kept (Q49).
func BenchmarkIntervals(b *testing.B) {
	s, err := Open(b.Context(), "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	now := time.Now()
	fillIntervals(b, s, now, 90*24*time.Hour, 160_000)
	for _, w := range []struct {
		name string
		d    time.Duration
	}{{"1h", time.Hour}, {"6h", 6 * time.Hour}, {"24h", 24 * time.Hour}, {"week", 7 * 24 * time.Hour}} {
		b.Run(w.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := s.Intervals(b.Context(), now.Add(-w.d)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

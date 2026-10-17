package store

import (
	"slices"
	"testing"
	"time"
)

// TestSamples: samples are read back by series, after a time; an empty batch is a no-op.
func TestSamples(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

	t.Run("empty batch", func(t *testing.T) {
		t.Parallel()
		if err := s.AddSamples(ctx, nil); err != nil {
			t.Fatalf("empty AddSamples: %v", err)
		}
	})

	var in []Sample
	for at := now.Add(-time.Minute); !at.After(now); at = at.Add(5 * time.Second) {
		in = append(in, Sample{Series: "task:w1", At: at, Seconds: 5, CPU: 50, Memory: 300 << 20})
	}
	if err := s.AddSamples(ctx, in); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		series    string
		from      time.Time
		wantCount int
		wantFirst time.Time
		wantMem   uint64
	}{
		{
			name:      "worker samples after 30s ago",
			series:    "task:w1",
			from:      now.Add(-30 * time.Second),
			wantCount: 6,
			wantFirst: now.Add(-25 * time.Second),
			wantMem:   300 << 20,
		},
		{
			name:      "unknown series",
			series:    "task:missing",
			from:      time.Time{},
			wantCount: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.Samples(ctx, tc.series, tc.from)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("Samples(%q) count = %d, want %d", tc.series, len(got), tc.wantCount)
			}
			if tc.wantCount > 0 {
				if got[0].At.UnixMilli() != tc.wantFirst.UnixMilli() || got[0].Memory != tc.wantMem {
					t.Errorf("first sample = %+v, want At=%v Memory=%d", got[0], tc.wantFirst, tc.wantMem)
				}
			}
		})
	}

	t.Run("migrates pre-self columns table", func(t *testing.T) {
		t.Parallel()
		s2, err := Open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer s2.Close()
		if _, err := s2.db.ExecContext(ctx, `DROP TABLE sample`); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.db.ExecContext(ctx, `CREATE TABLE sample (
			series TEXT NOT NULL,
			at INTEGER NOT NULL,
			seconds INTEGER NOT NULL,
			cpu REAL NOT NULL,
			memory INTEGER NOT NULL,
			djinn_cpu REAL NOT NULL,
			djinn_memory INTEGER NOT NULL
		)`); err != nil {
			t.Fatal(err)
		}
		if err := s2.migrate(ctx, nil); err != nil {
			t.Fatalf("migrate legacy sample table: %v", err)
		}
		cols := strs(t, s2, `SELECT name FROM pragma_table_info('sample')`)
		if !slices.Contains(cols, "self_cpu") || !slices.Contains(cols, "self_memory") {
			t.Errorf("sample columns after migration = %v", cols)
		}
	})
}

// TestPruneSamples: past Fine samples are rolled up by the minute, keeping the highest values; past Keep they go,
// and beyond Max the oldest go first.
func TestPruneSamples(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := open(t)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

	var in []Sample
	// Three hours of readings every 5 s of the machine, and a worker's last minute.
	for at := now.Add(-3 * time.Hour); !at.After(now); at = at.Add(5 * time.Second) {
		cpu := 10.0
		if at.Equal(now.Add(-90*time.Minute + 10*time.Second)) {
			cpu = 95 // A peak, an hour and a half ago.
		}
		in = append(in, Sample{Series: "machine", At: at, Seconds: 5, CPU: cpu, Memory: 1 << 30, DjinnMemory: 1 << 20})
	}
	for at := now.Add(-time.Minute); !at.After(now); at = at.Add(5 * time.Second) {
		in = append(in, Sample{Series: "task:w1", At: at, Seconds: 5, CPU: 50, Memory: 300 << 20})
	}
	if err := s.AddSamples(ctx, in); err != nil {
		t.Fatal(err)
	}

	t.Run("rolls up past Fine and drops past Keep", func(t *testing.T) {
		if err := s.PruneSamples(ctx, now, Retention{Fine: time.Hour, Keep: 2 * time.Hour, Max: 100000}); err != nil {
			t.Fatal(err)
		}
		all, err := s.Samples(ctx, "machine", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		// Two hours kept: one of minutes rolled up, then an hour of readings (12 a minute, and the one at now).
		minutes, fine, peak := 0, 0, 0.0
		for _, x := range all {
			if x.At.Before(now.Add(-2 * time.Hour)) {
				t.Fatalf("a sample older than Keep: %v", x)
			}
			if x.Seconds == 60 {
				minutes++
				peak = max(peak, x.CPU)
			} else {
				fine++
			}
		}
		if minutes != 60 || fine != 12*60+1 || peak != 95 {
			t.Errorf("after pruning: %d minutes (want 60), %d readings (want 721), peak %v (want 95)", minutes, fine, peak)
		}
	})

	t.Run("caps to Max removing oldest first", func(t *testing.T) {
		if err := s.PruneSamples(ctx, now, Retention{Fine: time.Hour, Keep: 2 * time.Hour, Max: 100}); err != nil {
			t.Fatal(err)
		}
		all, _ := s.Samples(ctx, "machine", time.Time{})
		worker, _ := s.Samples(ctx, "task:w1", time.Time{})
		if len(all)+len(worker) != 100 || len(worker) != 13 || !all[len(all)-1].At.Equal(now) {
			t.Errorf("capped to 100: %d machine samples, %d of the worker", len(all), len(worker))
		}
	})
}

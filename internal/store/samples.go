package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Sample is a measure of a series at a time: the machine's CPU and memory with Djinn's own share, or a worker's. A
// sample is an observation, not a change of the plan: it is kept apart from the journal and from the entities, and
// no commit hook hears of it.
type Sample struct {
	// The series: "machine", or "task:" and the task's id.
	Series string
	At     time.Time
	// Seconds the sample stands for: 5 for a reading, 60 for a minute rolled up (its highest values).
	Seconds     int32
	CPU         float64
	Memory      uint64
	DjinnCPU    float64
	DjinnMemory uint64
}

func (s *Store) migrateSamples(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sample (
		series TEXT NOT NULL,
		at INTEGER NOT NULL,
		seconds INTEGER NOT NULL,
		cpu REAL NOT NULL,
		memory INTEGER NOT NULL,
		djinn_cpu REAL NOT NULL,
		djinn_memory INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create the samples: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS sample_series_at ON sample (series, at)`); err != nil {
		return fmt.Errorf("index the samples: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS sample_at ON sample (at)`); err != nil {
		return fmt.Errorf("index the samples: %w", err)
	}
	return nil
}

// AddSamples writes samples, in one transaction.
func (s *Store) AddSamples(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO sample (series, at, seconds, cpu, memory, djinn_cpu, djinn_memory) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, x := range samples {
			if _, err := stmt.ExecContext(ctx, x.Series, x.At.UnixMilli(), x.Seconds, x.CPU, int64(x.Memory), x.DjinnCPU,
				int64(x.DjinnMemory)); err != nil {
				return err
			}
		}
		return nil
	})
}

// Samples reads the samples of series measured after from, the oldest first.
func (s *Store) Samples(ctx context.Context, series string, from time.Time) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, seconds, cpu, memory, djinn_cpu, djinn_memory FROM sample WHERE series = ? AND at > ? ORDER BY at`,
		series, from.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var at, memory, djinnMemory int64
		x := Sample{Series: series}
		if err := rows.Scan(&at, &x.Seconds, &x.CPU, &memory, &x.DjinnCPU, &djinnMemory); err != nil {
			return nil, err
		}
		x.At, x.Memory, x.DjinnMemory = time.UnixMilli(at), uint64(memory), uint64(djinnMemory)
		out = append(out, x)
	}
	return out, rows.Err()
}

// Retention is how long the samples are kept, and how many at most.
type Retention struct {
	// Readings younger than Fine stay as read; older ones are rolled up by the minute, keeping each minute's highest
	// values, so a peak stays readable.
	Fine time.Duration
	// Samples older than Keep go.
	Keep time.Duration
	// The most samples kept: beyond it, the oldest go first.
	Max int
}

// PruneSamples rolls up and removes samples as r says, now being the time of the last reading.
func (s *Store) PruneSamples(ctx context.Context, now time.Time, r Retention) error {
	// A whole minute: a minute is never split between a reading kept and its roll-up.
	fine := now.Add(-r.Fine).Truncate(time.Minute).UnixMilli()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sample (series, at, seconds, cpu, memory, djinn_cpu, djinn_memory)
			SELECT series, (at / 60000) * 60000, 60, MAX(cpu), MAX(memory), MAX(djinn_cpu), MAX(djinn_memory)
			FROM sample WHERE seconds < 60 AND at < ? GROUP BY series, at / 60000`, fine); err != nil {
			return fmt.Errorf("roll up the samples: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sample WHERE seconds < 60 AND at < ?`, fine); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sample WHERE at < ?`, now.Add(-r.Keep).UnixMilli()); err != nil {
			return err
		}
		if r.Max > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sample WHERE rowid IN (
				SELECT rowid FROM sample ORDER BY at LIMIT MAX(0, (SELECT COUNT(*) FROM sample) - ?))`, r.Max); err != nil {
				return err
			}
		}
		return nil
	})
}

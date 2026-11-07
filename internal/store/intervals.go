package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Interval is a gate held, from its request to its release, kept as its holder saw it (machinev1.GateInterval, in
// Data). Like a sample, it is an observation, not a change of the plan: kept apart from the journal and the entities,
// no commit hook hears of it, and it goes after a week (PruneIntervals).
type Interval struct {
	Released time.Time
	Data     []byte
}

func (s *Store) migrateIntervals(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS gate_interval (
		released INTEGER NOT NULL,
		data BLOB NOT NULL
	)`); err != nil {
		return fmt.Errorf("create the gate intervals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS gate_interval_released ON gate_interval (released)`); err != nil {
		return fmt.Errorf("index the gate intervals: %w", err)
	}
	return nil
}

// AddInterval writes a gate interval.
func (s *Store) AddInterval(ctx context.Context, x Interval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO gate_interval (released, data) VALUES (?, ?)`, x.Released.UnixMilli(), x.Data)
	return err
}

// Intervals reads the gate intervals released after from, the first released first.
func (s *Store) Intervals(ctx context.Context, from time.Time) ([]Interval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT released, data FROM gate_interval WHERE released > ? ORDER BY released`,
		from.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Interval
	for rows.Next() {
		var at int64
		var x Interval
		if err := rows.Scan(&at, &x.Data); err != nil {
			return nil, err
		}
		x.Released = time.UnixMilli(at)
		out = append(out, x)
	}
	return out, rows.Err()
}

// PruneIntervals removes the gate intervals released before before.
func (s *Store) PruneIntervals(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM gate_interval WHERE released < ?`, before.UnixMilli())
	return err
}

package machine

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// IntervalRetention is how long the gate intervals are kept: a week, the longest range of the series.
const IntervalRetention = 7 * 24 * time.Hour

// Intervals keeps the gate intervals in the store, where they outlive a restart (G10 of L04), for IntervalRetention.
type Intervals struct {
	db     *store.Store
	mu     sync.Mutex
	pruned time.Time
}

// NewIntervals keeps the gate intervals in db.
func NewIntervals(db *store.Store) *Intervals { return &Intervals{db: db} }

// Add keeps a gate interval released; one released over IntervalRetention ago goes, every 10 minutes. A failure is
// logged: the gates go on.
func (l *Intervals) Add(ctx context.Context, iv *machinev1.GateInterval) {
	data, err := proto.Marshal(iv)
	if err == nil {
		err = l.db.AddInterval(ctx, store.Interval{Released: iv.GetReleaseTime().AsTime(), Data: data})
	}
	if err != nil {
		logx.L().Warn("keep a gate interval", "gate", iv.GetGate(), "err", err)
	}
	l.mu.Lock()
	now := time.Now()
	prune := now.Sub(l.pruned) >= 10*time.Minute
	if prune {
		l.pruned = now
	}
	l.mu.Unlock()
	if prune {
		if err := l.db.PruneIntervals(ctx, now.Add(-IntervalRetention)); err != nil {
			logx.L().Warn("prune the gate intervals", "err", err)
		}
	}
}

// Since gives the gate intervals released after cutoff, the first released first; none when the store fails.
func (l *Intervals) Since(ctx context.Context, cutoff time.Time) []*machinev1.GateInterval {
	rows, err := l.db.Intervals(ctx, cutoff)
	if err != nil {
		logx.L().Warn("read the gate intervals", "err", err)
		return nil
	}
	out := make([]*machinev1.GateInterval, 0, len(rows))
	for _, r := range rows {
		iv := &machinev1.GateInterval{}
		if err := proto.Unmarshal(r.Data, iv); err != nil {
			continue
		}
		out = append(out, iv)
	}
	return out
}

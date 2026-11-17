package machine

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestIntervalsRetention: the gate intervals stay three months (Q49): one released 8 days ago is kept, one released
// 91 days ago goes at the next prune.
func TestIntervalsRetention(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := store.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	for _, x := range []struct {
		gate string
		ago  time.Duration
	}{{"old", 91 * 24 * time.Hour}, {"week", 8 * 24 * time.Hour}} {
		data, err := proto.Marshal(&machinev1.GateInterval{Gate: x.gate, ReleaseTime: timestamppb.New(now.Add(-x.ago))})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AddInterval(ctx, store.Interval{Released: now.Add(-x.ago), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	l := NewIntervals(db)
	l.Add(ctx, &machinev1.GateInterval{Gate: "now", ReleaseTime: timestamppb.New(now)})
	var got []string
	for _, iv := range l.Since(ctx, time.Time{}) {
		got = append(got, iv.GetGate())
	}
	if len(got) != 2 || got[0] != "week" || got[1] != "now" {
		t.Errorf("gates kept = %v, want [week now]", got)
	}
}

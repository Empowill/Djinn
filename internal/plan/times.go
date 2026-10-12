package plan

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// FillTimes computes on the server and sets the durations on tasks:
// - running: the worker's process ran and did not wait for a gate;
// - waiting_gate: while running, the time it waited for a gate to be granted;
// - paused: every gap between the first start and now or the end where the task was not running;
// - waiting_before_start: the wait before the first start (from planned/create_time to first start, or to now).
// An azima's time sums its parts' work times.
func FillTimes(ctx context.Context, s *store.Store, tasks []*planv1.Task, now time.Time) error {
	if len(tasks) == 0 {
		return nil
	}

	for _, t := range tasks {
		if IsAzima(t) {
			continue
		}
		if err := fillLeafTimes(ctx, s, t, now); err != nil {
			return err
		}
	}

	if !slices.ContainsFunc(tasks, IsAzima) {
		return nil
	}

	wishes := map[string]bool{}
	tasksByWish := map[string][]*planv1.Task{}
	for _, t := range tasks {
		if IsAzima(t) && t.GetWishId() != "" {
			wishes[t.GetWishId()] = true
		}
		if t.GetWishId() != "" {
			tasksByWish[t.GetWishId()] = append(tasksByWish[t.GetWishId()], t)
		}
	}

	for wishID := range wishes {
		wishTasks := tasksByWish[wishID]
		if s != nil {
			all, err := store.List[*planv1.Task](ctx, s, store.Where{"wish_id": wishID})
			if err == nil && len(all) > len(wishTasks) {
				taskMap := make(map[string]*planv1.Task, len(all))
				for _, t := range wishTasks {
					taskMap[t.GetId()] = t
				}
				for _, t := range all {
					if _, exists := taskMap[t.GetId()]; !exists {
						if !IsAzima(t) {
							_ = fillLeafTimes(ctx, s, t, now)
						}
						taskMap[t.GetId()] = t
					}
				}
				wishTasks = make([]*planv1.Task, 0, len(taskMap))
				for _, t := range taskMap {
					wishTasks = append(wishTasks, t)
				}
			}
		}

		fillWishAzimaTimes(wishTasks)
	}

	return nil
}

func fillLeafTimes(ctx context.Context, s *store.Store, t *planv1.Task, now time.Time) error {
	var firstStart time.Time
	if t.FirstStartTime != nil {
		firstStart = t.FirstStartTime.AsTime()
	} else if t.StartTime != nil {
		firstStart = t.StartTime.AsTime()
		t.FirstStartTime = t.StartTime
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && t.LastActivityTime == nil {
		t.LastActivityTime = t.StartTime
	}

	switch {
	case !firstStart.IsZero() && t.CreateTime != nil:
		w := firstStart.Sub(t.CreateTime.AsTime())
		if w < 0 {
			w = 0
		}
		t.WaitingBeforeStart = durationpb.New(w)
	case t.CreateTime != nil:
		w := now.Sub(t.CreateTime.AsTime())
		if w < 0 {
			w = 0
		}
		t.WaitingBeforeStart = durationpb.New(w)
	default:
		t.WaitingBeforeStart = durationpb.New(0)
	}

	if firstStart.IsZero() {
		t.Running = durationpb.New(0)
		t.WaitingGate = durationpb.New(0)
		t.Paused = durationpb.New(0)
		return nil
	}

	var totalRunDuration time.Duration
	if len(t.Runs) > 0 {
		for _, r := range t.Runs {
			rStart := r.GetStartTime().AsTime()
			var rEnd time.Time
			switch {
			case r.EndTime != nil:
				rEnd = r.EndTime.AsTime()
			case t.Status == planv1.TaskStatus_TASK_STATUS_RUNNING:
				rEnd = now
			case t.EndTime != nil:
				rEnd = t.EndTime.AsTime()
			default:
				rEnd = rStart
			}
			if rEnd.After(rStart) {
				totalRunDuration += rEnd.Sub(rStart)
			}
		}
	} else if t.StartTime != nil {
		rStart := t.StartTime.AsTime()
		var rEnd time.Time
		switch {
		case t.EndTime != nil:
			rEnd = t.EndTime.AsTime()
		case t.Status == planv1.TaskStatus_TASK_STATUS_RUNNING:
			rEnd = now
		default:
			rEnd = rStart
		}
		if rEnd.After(rStart) {
			totalRunDuration += rEnd.Sub(rStart)
		}
	}

	var gateWait time.Duration
	if s != nil && t.GetId() != "" {
		gateWait = waitedGates(ctx, s, t, now)
	}

	if gateWait > totalRunDuration {
		gateWait = totalRunDuration
	}
	t.WaitingGate = durationpb.New(gateWait)

	running := totalRunDuration - gateWait
	if running < 0 {
		running = 0
	}
	t.Running = durationpb.New(running)

	var finalEnd time.Time
	if t.EndTime != nil {
		finalEnd = t.EndTime.AsTime()
	} else {
		finalEnd = now
	}
	elapsed := time.Duration(0)
	if finalEnd.After(firstStart) {
		elapsed = finalEnd.Sub(firstStart)
	}
	paused := elapsed - totalRunDuration
	if paused < 0 {
		paused = 0
	}
	t.Paused = durationpb.New(paused)

	return nil
}

func fillWishAzimaTimes(tasks []*planv1.Task) {
	byPartOf := map[string][]*planv1.Task{}
	for _, t := range tasks {
		if p := t.GetPartOf(); p != "" {
			byPartOf[p] = append(byPartOf[p], t)
		}
	}

	var sumParts func(id string, seen map[string]bool) (running, waitingGate, paused, waitingBeforeStart time.Duration, firstStart time.Time)
	sumParts = func(id string, seen map[string]bool) (running, waitingGate, paused, waitingBeforeStart time.Duration, firstStart time.Time) {
		if seen[id] {
			return 0, 0, 0, 0, time.Time{}
		}
		seen[id] = true
		for _, part := range byPartOf[id] {
			if IsAzima(part) {
				r, wg, p, wb, fs := sumParts(part.GetId(), seen)
				running += r
				waitingGate += wg
				paused += p
				waitingBeforeStart += wb
				if !fs.IsZero() && (firstStart.IsZero() || fs.Before(firstStart)) {
					firstStart = fs
				}
			} else {
				running += part.GetRunning().AsDuration()
				waitingGate += part.GetWaitingGate().AsDuration()
				paused += part.GetPaused().AsDuration()
				waitingBeforeStart += part.GetWaitingBeforeStart().AsDuration()
				var partStart time.Time
				if part.FirstStartTime != nil {
					partStart = part.FirstStartTime.AsTime()
				} else if part.StartTime != nil {
					partStart = part.StartTime.AsTime()
				}
				if !partStart.IsZero() && (firstStart.IsZero() || partStart.Before(firstStart)) {
					firstStart = partStart
				}
			}
		}
		return running, waitingGate, paused, waitingBeforeStart, firstStart
	}

	for _, t := range tasks {
		if !IsAzima(t) {
			continue
		}
		r, wg, p, wb, fs := sumParts(t.GetId(), map[string]bool{})
		t.Running = durationpb.New(r)
		t.WaitingGate = durationpb.New(wg)
		t.Paused = durationpb.New(p)
		t.WaitingBeforeStart = durationpb.New(wb)
		if !fs.IsZero() && t.FirstStartTime == nil {
			t.FirstStartTime = timestamppb.New(fs)
		}
	}
}

// gateTallies keeps, by store and task, what the task's gate events add up to: its events only grow, so a fill reads
// the ones past the last it read, never all of them again (W504: a task list read every event of every task, half a
// gigabyte, while the command line waited for the store).
var gateTallies sync.Map // gateKey -> *gateTally

// gateKey is a task of a store: the store held keeps its address from being reused by another.
type gateKey struct {
	s    *store.Store
	task string
}

// gateTally is the gate events of a task read so far.
type gateTally struct {
	mu     sync.Mutex
	seq    int64                // the last event read past
	asked  map[string]time.Time // by gate, asked and not taken yet
	waited time.Duration        // between each gate asked and taken
}

// gateKind is the encoded kind of a gate event: the store skips, without decoding them, the events that lack it.
var gateKind = func() []byte {
	b, _ := proto.Marshal(&planv1.TaskEvent{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE})
	return b
}()

// waitedGates is how long task t waited for its gates: between each gate asked and taken, and since a gate asked
// still not taken while it runs. It reads the gate events past those it read before; the error of a read leaves the
// last tally.
func waitedGates(ctx context.Context, s *store.Store, t *planv1.Task, now time.Time) time.Duration {
	v, _ := gateTallies.LoadOrStore(gateKey{s, t.GetId()}, &gateTally{asked: map[string]time.Time{}})
	tally := v.(*gateTally)
	tally.mu.Lock()
	defer tally.mu.Unlock()
	var last int64
	err := store.Latest(ctx, s, store.Where{"task_id": t.GetId()}, func(ev *planv1.TaskEvent) bool {
		last = ev.GetSeq()
		return false
	})
	if err == nil && last > tally.seq {
		events, err := store.ListAfter[*planv1.TaskEvent](ctx, s, store.Where{"task_id": t.GetId()},
			store.After{Field: "seq", From: tally.seq, Contains: [][]byte{gateKind, []byte("gate ")}})
		if err == nil {
			for _, ev := range events {
				if ev.GetSeq() <= last { // Those past it are read with the events around them, next time.
					tally.add(ev)
				}
			}
			tally.seq = last
		}
	}
	wait := tally.waited
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
		for _, askedTime := range tally.asked {
			if now.After(askedTime) {
				wait += now.Sub(askedTime)
			}
		}
	}
	return wait
}

// add counts ev, if it is a gate asked or taken.
func (g *gateTally) add(ev *planv1.TaskEvent) {
	if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_GATE {
		return
	}
	name, action, ok := strings.Cut(strings.TrimPrefix(ev.GetText(), "gate "), ": ")
	if !ok || !strings.HasPrefix(ev.GetText(), "gate ") {
		return
	}
	at := ev.GetCreateTime().AsTime()
	switch action {
	case "asked":
		g.asked[name] = at
	case "taken":
		if askedTime, ok := g.asked[name]; ok {
			if at.After(askedTime) {
				g.waited += at.Sub(askedTime)
			}
			delete(g.asked, name)
		}
	}
}

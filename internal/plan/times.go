package plan

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
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

	if err := fillLeavesTimes(ctx, s, tasks, now); err != nil {
		return err
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
				var others []*planv1.Task
				for _, t := range all {
					if _, exists := taskMap[t.GetId()]; !exists {
						others = append(others, t)
						taskMap[t.GetId()] = t
					}
				}
				_ = fillLeavesTimes(ctx, s, others, now)
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

// fillLeavesTimes sets the times of the tasks that are not azimas, with the gate waits of those that started.
func fillLeavesTimes(ctx context.Context, s *store.Store, tasks []*planv1.Task, now time.Time) error {
	var started []*planv1.Task
	for _, t := range tasks {
		if !IsAzima(t) && t.GetId() != "" && (t.FirstStartTime != nil || t.StartTime != nil) {
			started = append(started, t)
		}
	}
	var waits map[string]*planv1.GateWait
	if s != nil && len(started) > 0 {
		var err error
		if waits, err = gateWaits(ctx, s, started); err != nil {
			return err
		}
	}
	for _, t := range tasks {
		if !IsAzima(t) {
			fillLeafTimes(t, waits[t.GetId()], now)
		}
	}
	return nil
}

// fillLeafTimes sets the times of t, a task that is not an azima, w its gate wait.
func fillLeafTimes(t *planv1.Task, w *planv1.GateWait, now time.Time) {
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
		return
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

	gateWait := waited(w, t, now)

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

// gateKind is the encoded kind of a gate event: the store skips, without decoding them, the events that lack it.
var gateKind = func() []byte {
	b, _ := proto.Marshal(&planv1.TaskEvent{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE})
	return b
}()

// Who counts the gate events of a task from before GateWait, as the journal records it.
const (
	gateActor  = "harness"
	gateMethod = "harness/gate-wait" // a task's gate wait was counted from its events; the request is the count
)

// CountGate counts ev, an event about to be written in tx, in the gate wait of its task. Djinn writes every event of
// a task through it (harness putEvents): the gate wait kept stays the sum of the task's gate events, which a task list
// then never reads (W283). A task from before it gets its count from the events written before ev.
func CountGate(ctx context.Context, tx *store.Tx, ev *planv1.TaskEvent) error {
	if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_GATE {
		return nil
	}
	w, kept, err := gateWait(ctx, tx, ev.GetTaskId())
	if err != nil {
		return err
	}
	if !addGate(w, ev) && kept {
		return nil
	}
	return tx.Put(w)
}

// gateWaits is the gate wait of each task, by id. Those from before GateWait are counted from their gate events, then
// kept, all in one transaction: the first list after an update reads their events once, and never again. Should
// keeping them fail, the list still has their count, and the next counts them again.
func gateWaits(ctx context.Context, s *store.Store, tasks []*planv1.Task) (map[string]*planv1.GateWait, error) {
	waits := make(map[string]*planv1.GateWait, len(tasks))
	var counted []*planv1.GateWait
	for _, t := range tasks {
		w, kept, err := gateWait(ctx, s, t.GetId())
		if err != nil {
			return nil, err
		}
		waits[t.GetId()] = w
		if !kept {
			counted = append(counted, w)
		}
	}
	if len(counted) == 0 {
		return waits, nil
	}
	err := s.Tx(ctx, func(tx *store.Tx) error {
		for _, w := range counted {
			// A gate event written since counted all the task's events itself (CountGate): its count stands.
			if kept, err := store.Get[*planv1.GateWait](ctx, tx, w.GetId()); err == nil {
				waits[w.GetId()] = kept
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := tx.Journal(gateActor, gateMethod, w); err != nil {
				return err
			}
			if err := tx.Put(w); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logx.L().Warn("keep the gate waits counted from the events", "tasks", len(counted), "err", err)
	}
	return waits, nil
}

// gateWait is the gate wait kept for the task, kept true; or, for a task from before GateWait, the count of its gate
// events, not kept yet.
func gateWait(ctx context.Context, r store.Reader, taskID string) (*planv1.GateWait, bool, error) {
	w, err := store.Get[*planv1.GateWait](ctx, r, taskID)
	if err == nil {
		return w, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	events, err := store.ListAfter[*planv1.TaskEvent](ctx, r, store.Where{"task_id": taskID},
		store.After{Field: "seq", Contains: [][]byte{gateKind, []byte("gate ")}})
	if err != nil {
		return nil, false, err
	}
	w = &planv1.GateWait{Id: taskID}
	for _, ev := range events {
		addGate(w, ev)
	}
	return w, false, nil
}

// waited is how long a task waited for its gates, from its gate wait w: between each gate asked and taken, and,
// while it runs, since each gate asked still not taken.
func waited(w *planv1.GateWait, t *planv1.Task, now time.Time) time.Duration {
	wait := w.GetWaited().AsDuration()
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
		for _, asked := range w.GetAsked() {
			if at := asked.AsTime(); now.After(at) {
				wait += now.Sub(at)
			}
		}
	}
	return wait
}

// addGate counts ev in w, if it is a gate asked or taken; it says whether w changed.
func addGate(w *planv1.GateWait, ev *planv1.TaskEvent) bool {
	if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_GATE {
		return false
	}
	name, action, ok := strings.Cut(strings.TrimPrefix(ev.GetText(), "gate "), ": ")
	if !ok || !strings.HasPrefix(ev.GetText(), "gate ") {
		return false
	}
	switch action {
	case "asked":
		if w.Asked == nil {
			w.Asked = map[string]*timestamppb.Timestamp{}
		}
		w.Asked[name] = ev.GetCreateTime()
		return true
	case "taken":
		asked, ok := w.GetAsked()[name]
		if !ok {
			return false
		}
		if at, askedAt := ev.GetCreateTime().AsTime(), asked.AsTime(); at.After(askedAt) {
			w.Waited = durationpb.New(w.GetWaited().AsDuration() + at.Sub(askedAt))
		}
		delete(w.Asked, name)
		return true
	}
	return false
}

package plan

import (
	"context"
	"slices"
	"strings"
	"time"

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
		events, err := store.List[*planv1.TaskEvent](ctx, s, store.Where{"task_id": t.GetId()})
		if err == nil {
			asked := make(map[string]time.Time)
			for _, ev := range events {
				if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_GATE {
					continue
				}
				text := ev.GetText()
				if strings.HasPrefix(text, "gate ") {
					parts := strings.SplitN(text[5:], ": ", 2)
					if len(parts) == 2 {
						name, action := parts[0], parts[1]
						evTime := ev.GetCreateTime().AsTime()
						switch action {
						case "asked":
							asked[name] = evTime
						case "taken":
							if askedTime, ok := asked[name]; ok {
								if evTime.After(askedTime) {
									gateWait += evTime.Sub(askedTime)
								}
								delete(asked, name)
							}
						}
					}
				}
			}
			if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
				for _, askedTime := range asked {
					if now.After(askedTime) {
						gateWait += now.Sub(askedTime)
					}
				}
			}
		}
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

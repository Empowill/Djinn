package dispatch

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestNetworkHolds (W285): while the network is down, no task starts, a watcher neither, each waiting for the machine
// (djinn task start may start it); once it is back, one task starts a pass, a correction first, and none when the
// harness resumed a paused worker in that pass already. A watcher takes no slot: it starts whatever the pace.
func TestNetworkHolds(t *testing.T) {
	now := time.Date(2026, 10, 17, 15, 30, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	task := func(id string, s planv1.TaskStatus, edit func(*planv1.Task)) *planv1.Task {
		t := &planv1.Task{Id: id, WishId: "w", Code: id, Status: s, Scheduled: true, CreateTime: timestamppb.New(now)}
		if edit != nil {
			edit(t)
		}
		return t
	}
	tasks := func() []*planv1.Task {
		return []*planv1.Task{
			task("new", planv1.TaskStatus_TASK_STATUS_PENDING, nil),
			task("cut", planv1.TaskStatus_TASK_STATUS_RESUMING, func(t *planv1.Task) { t.WaitReason = "network down since 15:12" }),
			task("repair", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) {
				t.Correction = &planv1.TaskCorrection{Attempt: 1}
			}),
			task("watch", planv1.TaskStatus_TASK_STATUS_PENDING, func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }),
		}
	}
	pass := func(n *Network) (started []string, why map[string]Decision) {
		why = map[string]Decision{}
		for _, d := range New(tasks(), []*planv1.Wish{wish}, nil, &Machine{Slots: 8, Rule: "test"}).At(now).WithNetwork(n).Pass() {
			if d.Why == "" && d.Failed == "" {
				started = append(started, d.Task.GetCode())
			}
			why[d.Task.GetCode()] = d
		}
		return started, why
	}

	down := "network down since 15:12: Djinn starts no worker until it is back"
	if started, why := pass(&Network{Down: down}); len(started) != 0 {
		t.Errorf("down: %v started", started)
	} else {
		for code, d := range why {
			if d.Why != down || !d.Machine || !d.Room {
				t.Errorf("down: %s %+v; want waiting for the machine, %q", code, d, down)
			}
		}
	}

	started, why := pass(&Network{OnePerPass: true})
	if strings.Join(started, ",") != "repair,watch" {
		t.Errorf("back: %v started; want the correction first, alone, and the watcher", started)
	}
	if d := why["cut"]; !strings.HasPrefix(d.Why, "the network is back: ") {
		t.Errorf("back: cut %+v; want its turn after the correction", d)
	}

	if started, _ := pass(&Network{OnePerPass: true, Spent: true}); strings.Join(started, ",") != "watch" {
		t.Errorf("back, a paused worker resumed in the pass: %v started; want the watcher alone", started)
	}
	if started, _ := pass(nil); len(started) != 4 {
		t.Errorf("up: %v started; want all", started)
	}
}

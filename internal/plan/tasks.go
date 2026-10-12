package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// NextTaskNumber is the code of a new task of the wish wishID lettered letter, W for work and T for an azima: the number
// after the highest one its tasks have or its deleted tasks had (Wish.retired_codes), so that a code is never given
// twice, deleted or not.
func NextTaskNumber(ctx context.Context, r store.Reader, wishID, letter string) (string, error) {
	wish, err := store.Get[*planv1.Wish](ctx, r, wishID)
	if err != nil {
		return "", err
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return "", err
	}
	codes := wish.GetRetiredCodes()
	for _, t := range tasks {
		codes = append(codes, t.GetCode())
	}
	last := 0
	for _, c := range codes {
		var n int
		if _, err := fmt.Sscanf(strings.ToUpper(c), letter+"%d", &n); err == nil && n > last {
			last = n
		}
	}
	return fmt.Sprintf("%s%d", letter, last+1), nil
}

// ClosesCycle tells whether task, waiting for deps and part of azima, would reach itself through tasks, following what
// each task depends on and the azima it is part of, and then says the cycle in codes ("W1 → W3 → W1"); "" when it
// would not.
func ClosesCycle(task *planv1.Task, deps []string, azima string, tasks []*planv1.Task) string {
	byID := map[string]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	edges := func(id string) []string {
		if id == task.GetId() {
			if azima != "" {
				return append(slices.Clip(deps), azima)
			}
			return deps
		}
		t := byID[id]
		if p := t.GetPartOf(); p != "" {
			return append(slices.Clip(t.GetDependsOn()), p)
		}
		return t.GetDependsOn()
	}
	// A path from one of the task's edges back to task closes a cycle.
	seen := map[string]bool{}
	var path []string
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == task.GetId() {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		path = append(path, id)
		for _, next := range edges(id) {
			if walk(next) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	for _, d := range edges(task.GetId()) {
		path = path[:0]
		if walk(d) {
			codes := []string{task.GetCode()}
			for _, id := range path {
				codes = append(codes, byID[id].GetCode())
			}
			return strings.Join(append(codes, task.GetCode()), " → ")
		}
	}
	return ""
}

package plan

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

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

var promptKind = func() []byte {
	b, _ := proto.Marshal(&planv1.TaskEvent{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT})
	return b
}()

// FillPrompts sets each task's Prompt from its last prompt event in r, and fills each watcher's request link.
func FillPrompts(ctx context.Context, r store.Reader, tasks []*planv1.Task) error {
	for _, t := range tasks {
		events, err := store.ListAfter[*planv1.TaskEvent](
			ctx, r, store.Where{"task_id": t.GetId()},
			store.After{Field: "seq", Contains: [][]byte{promptKind}},
		)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			t.Prompt = events[len(events)-1].GetText()
		}
	}
	fillWatchLinks(ctx, r, tasks)
	return nil
}

var watchRequest = regexp.MustCompile(
	`(?i)(?:--?(?:mr|pr)(?:=|\s+)|\b(?:mr|pr)(?:\s+view)?\s*[!#]?|babysit-(?:mr|pr)/\S+\s+|/(?:pull|(?:-/)?merge_requests)/|(?:^|[\s(\[])[!#])(\d+)\b`,
)

// watchRequestNumber extracts a pull or merge request number named in s (e.g. "-mr 2863", "MR !2863", "PR #40").
func watchRequestNumber(s string) int {
	m := watchRequest.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// fillWatchLinks gives each watcher in tasks a request URL in order: its own babysit link, its azima's PR, or the
// MR/PR its command or title names on the project's forge.
func fillWatchLinks(ctx context.Context, r store.Reader, tasks []*planv1.Task) {
	var byID map[string]*planv1.Task
	var remotes map[string]string
	taskByID := func(id string) *planv1.Task {
		if id == "" {
			return nil
		}
		if byID == nil {
			byID = make(map[string]*planv1.Task, len(tasks))
			for _, t := range tasks {
				byID[t.GetId()] = t
			}
		}
		if t, ok := byID[id]; ok {
			return t
		}
		t, err := store.Get[*planv1.Task](ctx, r, id)
		if err != nil {
			byID[id] = nil
			return nil
		}
		byID[id] = t
		return t
	}
	projectRemote := func(projectID string) string {
		if projectID == "" {
			return ""
		}
		proj, err := store.Get[*planv1.Project](ctx, r, projectID)
		if err != nil {
			return ""
		}
		rem := proj.GetRemote()
		if rem == "" && proj.GetDirectory() != "" {
			rem = remoteOf(proj.GetDirectory())
		}
		return rem
	}
	taskRemote := func(t *planv1.Task) string {
		if rem := projectRemote(t.GetProjectId()); rem != "" {
			return rem
		}
		wishID := t.GetWishId()
		if wishID == "" {
			return ""
		}
		if remotes == nil {
			remotes = map[string]string{}
		}
		if rem, ok := remotes[wishID]; ok {
			return rem
		}
		var rem string
		if wish, err := store.Get[*planv1.Wish](ctx, r, wishID); err == nil {
			for _, pid := range wish.GetProjectIds() {
				if rem = projectRemote(pid); rem != "" {
					break
				}
			}
		}
		remotes[wishID] = rem
		return rem
	}
	for _, t := range tasks {
		if !watches(t) || t.GetBabysit().GetUrl() != "" {
			continue
		}
		var url string
		for p := taskByID(t.GetPartOf()); p != nil && url == ""; p = taskByID(p.GetPartOf()) {
			url = p.GetAzima().GetPr().GetUrl()
		}
		if url == "" {
			n := watchRequestNumber(t.GetPrompt())
			if n == 0 {
				n = watchRequestNumber(t.GetTitle())
			}
			if n > 0 {
				url = forgeRequestURL(taskRemote(t), n)
			}
		}
		if url != "" {
			if t.Babysit == nil {
				t.Babysit = &planv1.TaskBabysit{Url: url}
			} else {
				t.Babysit.Url = url
			}
		}
	}
}

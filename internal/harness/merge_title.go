package harness

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Merge titles: a worker Djinn starts to bring work into the integration branch (a correction, a review) is titled
// from the work it brings, in words, so a person sees at a glance which work is being merged: "Merge: " and the titles
// of the original tasks it serves, followed back through the corrections and reviews before it, their codes after
// the words, then what failed. One line.

// mergeWords is how many characters of the served tasks' titles a merge title keeps.
const mergeWords = 60

// servedTasks are the original tasks the work of group serves: each correction or review followed back, through the
// corrections and reviews before it, to tasks that are neither, in order and once each. A task that cannot be read
// stands for itself.
func servedTasks(ctx context.Context, r store.Reader, group []*planv1.Task) []*planv1.Task {
	var out []*planv1.Task
	seen := map[string]bool{}
	var walk func(t *planv1.Task)
	walk = func(t *planv1.Task) {
		if seen[t.GetId()] {
			return
		}
		seen[t.GetId()] = true
		from := servedIDs(t)
		if len(from) == 0 {
			out = append(out, t)
			return
		}
		read := false
		for _, id := range from {
			if seen[id] {
				read = true
				continue
			}
			if s, err := store.Get[*planv1.Task](ctx, r, id); err == nil {
				read = true
				walk(s)
			}
		}
		if !read {
			out = append(out, t) // Nothing behind it could be read: it stands for its work.
		}
	}
	for _, t := range group {
		walk(t)
	}
	return out
}

// servedIDs are the tasks t, a correction or review worker, brings the work of; none for any other task.
func servedIDs(t *planv1.Task) []string {
	if c := t.GetCorrection(); c != nil {
		return c.GetFailure().GetTaskIds()
	}
	return t.GetReview().GetTaskIds()
}

// mergeTitle is the title of a worker that brings the work of tasks into the integration branch, what failed:
// "Merge: Antigravity workers may read web pages (W401) — tests".
func mergeTitle(tasks []*planv1.Task, what string) string {
	titles := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if s := oneLine(t.GetTitle()); s != "" {
			titles = append(titles, s)
		}
	}
	words := clipWords(strings.Join(titles, "; "), mergeWords)
	if c := codes(tasks); c != "" {
		words = strings.TrimSpace(words + " (" + c + ")")
	}
	return mergeLine(words, what)
}

// mergeLine is a merge title of the work words, what failed.
func mergeLine(words, what string) string {
	if what == "" {
		return "Merge: " + words
	}
	return "Merge: " + words + " — " + what
}

// failureWhat says what failed in f, short: "tests", "conflict in docs/agent-protocol.md and 2 more files".
func failureWhat(f *planv1.IntegrationFailure) string {
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		return "tests"
	}
	files := f.GetFiles()
	switch len(files) {
	case 0:
		return "conflict"
	case 1:
		return "conflict in " + files[0]
	case 2:
		return fmt.Sprintf("conflict in %s and 1 more file", files[0])
	}
	return fmt.Sprintf("conflict in %s and %d more files", files[0], len(files)-1)
}

var (
	goFail         = regexp.MustCompile(`--- FAIL: (\S+)`)
	goFailPackage  = regexp.MustCompile(`(?m)^FAIL\s+(\S+)\s`)
	playwrightFail = regexp.MustCompile(`(?m)^\s*(?:\d+\)|✘)\s+(?:\[[^\]]+\]\s+›\s+)?(\S+\.spec\.[jt]s)`)
)

// failedChecks names what failed in a push check's output, short: its failing Go tests or packages, or its failing
// specs; else the check's name, out of the failure's reason ("test: exit status 1").
func failedChecks(f *planv1.IntegrationFailure) string {
	out := f.GetOutput()
	var names []string
	add := func(s string) {
		if !slices.Contains(names, s) {
			names = append(names, s)
		}
	}
	for _, m := range goFail.FindAllStringSubmatch(out, -1) {
		add(m[1])
	}
	if len(names) == 0 {
		for _, m := range goFailPackage.FindAllStringSubmatch(out, -1) {
			add(path.Base(m[1]))
		}
	}
	for _, m := range playwrightFail.FindAllStringSubmatch(out, -1) {
		add(path.Base(m[1]))
	}
	if len(names) == 0 {
		name, _, _ := strings.Cut(headline(f.GetReason()), ":")
		return strings.TrimSpace(name)
	}
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	}
	return strings.Join(names, ", ")
}

// oneLine is s on one line, its spaces collapsed.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clipWords is s cut to about n characters at a word, "..." saying it was cut.
func clipWords(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:—-") + "..."
}

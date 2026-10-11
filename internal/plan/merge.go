package plan

import planv1 "github.com/empowill/djinn/gen/go/plan/v1"

// Merges are the workers Djinn starts by itself to bring work into an integration branch: a correction (its merge
// conflicted, or its tests or push checks failed) or a review (of work left not committed). A merge is shown, and
// counted, with the original task it serves (src/data/flight.ts mergeNest), not as a peer of its azima's work.

// IsMerge tells a correction or review worker.
func IsMerge(t *planv1.Task) bool { return t.GetCorrection() != nil || t.GetReview() != nil }

// mergeOf are the tasks a merge brings the work of: none for any other task.
func mergeOf(t *planv1.Task) []string {
	if c := t.GetCorrection(); c != nil {
		return c.GetFailure().GetTaskIds()
	}
	return t.GetReview().GetTaskIds()
}

// MergeServes are the original tasks the merge t serves, by id: the tasks it brings the work of, earlier merges among
// them followed back to tasks that are no merges, in order and once each. byID gives the tasks; one it lacks is left out.
func MergeServes(t *planv1.Task, byID map[string]*planv1.Task) []string {
	var out []string
	seen := map[string]bool{t.GetId(): true}
	var walk func(ids []string)
	walk = func(ids []string) {
		for _, id := range ids {
			s := byID[id]
			if seen[id] || s == nil {
				continue
			}
			seen[id] = true
			if IsMerge(s) {
				walk(mergeOf(s))
			} else {
				out = append(out, id)
			}
		}
	}
	walk(mergeOf(t))
	return out
}

// MergeHost is the task the merge t is shown and counted with: the first original task it serves that is part of
// the same azima as t (or, like t, of none); "" for any other task, or a merge that serves none there (a push or main
// branch correction).
func MergeHost(t *planv1.Task, byID map[string]*planv1.Task) string {
	if !IsMerge(t) {
		return ""
	}
	for _, id := range MergeServes(t, byID) {
		if byID[id].GetPartOf() == t.GetPartOf() {
			return id
		}
	}
	return ""
}

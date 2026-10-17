---
name: conflict-free-dag
description: The lead owns the wish's DAG; before each spawn read the open work (task list --open) and its overlaps (task overlaps), send work on the same files to the task that has them or order it --after, never side by side; write scopes when known; big work a chain, not a fan; never spawn from a redirected integration branch
type: feedback
---

The lead owns the wish's DAG: two workers that collide on a file are the plan's doing. Before each spawn, read what
runs and what comes (`djinn task list --wish-id <wish> --open --short`) and the files it touches
(`djinn task overlaps <wish>`, or the Overlaps section of `djinn wish brief`). Work on files an open task touches
never runs beside it: send it to that task (`djinn task update <task> --prompt` while planned, `djinn task send
<task>` while it runs), or spawn it `--after` that task. Give `--write-scopes` when you know the paths, so that a
planned task's overlaps show before it starts. Big work is a chain of small steps, each merged and green before the
next, never a fan of tasks on the same packages; work on other files still runs side by side, never in a naive
sequential line (`W1 -> W2 -> W3 -> W4`) just to be safe. A conflict or a red merge means re-sequence, not only
correct.

**Why:** the developer, 11/10/2026: "your role is to keep the DAG of tasks and azimas free of conflicts, to ease the
agents' work along an iterative and de-risked path." That night chains of conflict corrections (W358 then W361, W362
then W363, W378 then W383) followed each other, and W399, spawned while the integration pointed at a sink branch,
carried the sink's junk into the integration branch. On 14/10/2026 they added that avoiding conflicts must never turn
the DAG into a sequential line. On 17/10/2026 merges still conflicted (W211 merging W148: 8 files; W194, W197 for
W132 and W130) because parallel tasks ran on the same files; they asked for the lead to own the DAG explicitly, and
for Djinn to show the overlaps (W230).

W230 measured it on the wish's last 50 merges (W182 to W234, 17/10/2026): 10 conflicts, and for all 10 a task that
touched a conflicting file was already open when the conflicting task started, so the overlaps would have shown each
one. None came from a stale base or main's merge. No task had write scopes (0 of 324). The overlaps also flag much
that merges clean: 33 of 44 branches overlapped an open task at their start, 10 conflicted. The hottest files
(`locales/*.json`, `tests/screens.test.mjs`, `internal/harness/harness.go`, the plan proto and its generated files)
overlap almost every pair; the conflicts were on screens (`src/question-column.tsx`, `src/wish-app.tsx`), the proto
with `internal/harness/schedule.go`, and docs pages. The conflicted work had also waited long between its end and its
merge (median 1.6 h against minutes): tasks started together at 04:43–05:25 merged at 09:44–10:54, after their
neighbours.

Example: W160 (the Djinn's eye) and W174 (a click brings back a wish's first tab) ran side by side on
`src/question-column.tsx`, which W154 also changed; both merges conflicted there, and W237 and W238 repaired them.
W174 `--after W160`, or sent to W160, would have merged clean.

**How to apply:** before each spawn, run the two commands above; an overlap on a screen, a proto or a package's core
file means bundle or order; one only on the shared lists (locales, the screens test) is weaker, since nearly every
task touches them. Never chain tasks in a naive line just to be safe, and never spawn while the wish's integration
branch is redirected (a quarantine). The brief's rule "You own the wish's DAG" says the same; this note is why.

Djinn enforces the repair side itself (W233, Q44 A, 17/10/2026): while a correction repairs the wish's integration
branch in a project (a merge in conflict or red, main's merge; planned, running, or its work waiting to go in), no new
worker of the wish starts there; each waits saying `waits for W225 to settle the conflict in feat/qol`. Reviews, the
urgent task and investigators still start; running workers go on and take the repaired branch by message. Corrections
and reviews take a slot at once, ahead of every planned task: on a full machine Djinn pauses the lowest work task
(never a merge, an urgent task, an investigator or a task started by hand) and resumes it after. So: never
`djinn task start` a correction, never mark ordinary work urgent to pass the hold, and use the wait to re-sequence what
touches the conflicting files. That morning W217, W219, W220, W226 and W228 to W231 waited only for CPU while W225
repaired feat/qol, and would have started on the broken branch; W223, W224 and W225 waited for a slot like any task
while conflicts piled up, and the lead started them by hand.

See also [[regression-test-per-incident]].

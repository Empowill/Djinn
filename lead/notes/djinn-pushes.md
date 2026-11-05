---
name: djinn-pushes
description: In standard mode Djinn pushes by itself, never ask the developer to push; per azima it pushes only the azimas' branches, so work of no azima is never pushed (a gap); spawn a task that needs a push only once the push is done
type: feedback
---

In a project's standard push mode (the default), Djinn pushes the wish's integration branch, or under the per-azima
strategy each azima's own branch, by itself, within the minute of the merge that makes a push due:

1. **An azima ends:** a task part of it is committed and none of its other parts is left (a part still open, running,
   failed or waiting to be committed holds it). Its watchers do not count: a babysit of its pull or merge request
   runs until the merge, so a correction committed while it watches pushes at once. The azima shows watching while
   its watcher runs and no work part of it is left: not done, what depends on it still waits.
2. **The cadence:** three tasks committed since the last push and more than an hour since it (a wish sets its own,
   `push_after_tasks` and `push_after_minutes`).
3. **Main merged into a pushed branch:** main merged into the wish's integration branch, or under the per-azima
   strategy into the branch of an azima not done, once that branch was pushed, makes it due with no task committed
   (Q243 and Q245 of the wish Dew 13 oct): its pull request's CI runs on main as it is. Such a branch left ahead of its remote, with no push check holding it, is a bug
   to report, not a move to ask for.

Under the per-azima strategy, Djinn pushes only the azimas' own branches (`djinn/Tnn-...`), never the wish's integration
branch, which on djinn is main itself. A task part of no azima is merged into that branch all the same, and its commits
then live only in this clone: never pushed, lost with the clone. That is a gap of Djinn, not a behaviour to rely on: no
work may end like this. Until Djinn keeps it from happening, the lead keeps it from happening: every task of such a
project is spawned part of an azima, and a commit found there is carried at once into an azima's pull request, then
reported to the developer as a bug: see [[never-push-main]].

The push checks run first; red, they hold the push and Djinn starts a correction. A held push comes to you as an
event (Decision Q196): you decide, the developer is not asked. A wish in ask mode has Djinn ask the push question
itself. Only in on-demand mode does a branch wait for the developer's own push.

**Why:** decision Q10 (A) of the wish on watchers (W41's tilasm L05, revised by W54): an azima whose pull request was
babysat never ended, so its corrections waited for the cadence, and a push looked like the developer's move.

**How to apply:** never ask the developer to push a branch Djinn pushes in standard mode, nor to check that it was
pushed: Djinn does it within the minute, or tells you why it holds. Ask only in on-demand mode, or for what Djinn never
pushes (a tag, a release).

A task that needs a push done before it can start (ask a review bot to review the pushed head again, answer review
threads citing a pushed commit) is spawned only once that push is done: when the pull request's watcher reports the
new head, not before. Spawned earlier, part of the azima whose push it waits for, it deadlocks: an open part holds the
azima's push, so the push waits for the task, and the task waits for the push. Spawned after the push, it is part of
that azima like any task. W359, part of T30 to have CodeRabbit review dolmen !2990 again after W356 merged into it
(fe7d0c65), was spawned before the push: it held that push for its 20 minutes of watching, then ended with nothing
done (05/11/2026).

See also [[never-push-main]], [[push-hold-deadlock]], [[no-obvious-questions]], [[blockers-as-questions]].

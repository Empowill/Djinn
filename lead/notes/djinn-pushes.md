---
name: djinn-pushes
description: When Djinn pushes a branch by itself; in standard mode never ask the developer to push one
type: feedback
---

In a project's standard push mode (the default), Djinn pushes the wish's integration branch, or under the per-azima
strategy each azima's own branch, by itself, within the minute of the merge that makes a push due:

1. **An azima ends:** a task part of it is committed and none of its other parts is left (a part still open, running,
   failed or waiting to be committed holds it). Its watchers do not count: a babysit of its pull or merge request
   runs until the merge, so a correction committed while it watches pushes at once. The azima still shows in progress
   while its watcher runs.
2. **The cadence:** three tasks committed since the last push and more than an hour since it (a wish sets its own,
   `push_after_tasks` and `push_after_minutes`).

The push checks run first; red, they hold the push and Djinn starts a correction. A held push comes to you as an
event (Decision Q196): you decide, the developer is not asked. A wish in ask mode has Djinn ask the push question
itself. Only in on-demand mode does a branch wait for the developer's own push.

**Why:** decision Q10 (A) of the wish on watchers (W41's tilasm L05, revised by W54): an azima whose pull request was
babysat never ended, so its corrections waited for the cadence, and a push looked like the developer's move.

**How to apply:** never ask the developer to push a branch Djinn pushes in standard mode, nor to check that it was
pushed: Djinn does it within the minute, or tells you why it holds. Ask only in on-demand mode, or for what Djinn never
pushes (a tag, a release).

See also [[push-hold-deadlock]], [[no-obvious-questions]], [[blockers-as-questions]].

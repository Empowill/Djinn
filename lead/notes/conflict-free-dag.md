---
name: conflict-free-dag
description: The lead keeps the graph of tasks and azimas free of conflicts, an iterative de-risked path; never spawn from a redirected integration branch
type: feedback
---

Keep the graph of tasks and azimas so that workers never collide: tasks touching the same files, protos, generated
files or tests go one after the other (`--after`, `--write-scopes`); large work goes in small steps, each merged and
green before the next. A conflict or a red merge means re-sequence, not only correct.

**Why:** the developer, 11/10/2026: "your role is to keep the DAG of tasks and azimas free of conflicts, to ease the
agents' work along an iterative and de-risked path." That night chains of conflict corrections (W358 then W361, W362
then W363, W378 then W383) followed each other, and W399, spawned while the integration pointed at a sink branch,
carried the sink's junk into the integration branch.

**How to apply:** before each spawn, look at what running and planned tasks touch, and add `--after` where they
overlap. Never spawn while the wish's integration branch is redirected (a quarantine). The brief's rule "Keep the
graph free of conflicts" says the same; this note is why.

See also [[regression-test-per-incident]].

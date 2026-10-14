---
name: conflict-free-dag
description: Keep the DAG free of conflicts while maximizing parallelism; never chain tasks in a sequential line, add --after only on shared files or protos, and never spawn from a redirected integration branch
type: feedback
---

Keep the graph of tasks and azimas so that workers never collide while maximizing parallelism in the DAG: never chain
tasks in a naive sequential line (`W1 -> W2 -> W3 -> W4`) just to be safe. Estimate the impacted packages and files at
a high level (without reading code) so multiple workers run concurrently, and add `--after` (or `--write-scopes`) only
between tasks that directly target the same files, protos, generated files or tests (Djinn already has merge conflict
resolution for minor overlaps). Large work goes in small steps, each merged and green before the next. A conflict or a
red merge means re-sequence, not only correct.

**Why:** the developer, 11/10/2026: "your role is to keep the DAG of tasks and azimas free of conflicts, to ease the
agents' work along an iterative and de-risked path." That night chains of conflict corrections (W358 then W361, W362
then W363, W378 then W383) followed each other, and W399, spawned while the integration pointed at a sink branch,
carried the sink's junk into the integration branch. On 14/10/2026, the developer added that avoiding conflicts must
never turn the DAG into a sequential line (`W1 -> W2 -> W3 -> W4`): estimate impacted packages and files at a high
level without reading code, run independent tasks concurrently, and rely on `--after` only where tasks directly target
the same file or proto (remembering Djinn already resolves minor merge overlaps).

**How to apply:** before each spawn, estimate at a high level (without reading code) which packages or files running
and planned tasks target: maximize parallelism so independent workers run side by side, and add `--after` only where
tasks directly touch the same file or proto. Never chain tasks in a naive line just to be safe, and never spawn while
the wish's integration branch is redirected (a quarantine). The brief's rule "Keep the graph free of conflicts" says
the same; this note is why.

See also [[regression-test-per-incident]].

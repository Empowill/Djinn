---
name: waiting-for-the-rest
description: A worker done with a final note "waiting for the rest" left a long run behind its turn - the run was stopped, its result lost; rerun it in the foreground
type: project
---

A task that ends done while its worker's final note says it waits ("I wait for the rest", "waiting for the results",
"the run goes on in the background") is not done: the worker ended its turn on a long run it started in the
background (a benchmark, a build, a monitor watching a job). The task ends with that turn: what the run gave is lost,
and so is the worktree it wrote to, which Djinn removes once the task is done.

**Why:** 16/10/2026, in a wish of another project: a worker started a paid benchmark as a Claude Code background task
with a monitor, then ended its turn saying "I wait for the rest". Ten minutes later Claude Code logged "Background
tasks still running 10m after the last turn (monitor ...); stopping them", the task ended done, Djinn removed its
worktree: 11.60 USD spent for nothing. W80 fixed both sides: Claude workers run with
`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`, so claude waits for its background work while the task lives, and every
worker's instructions say never to end a turn while a long run is going, but to wait in the foreground in commands
under 9 minutes each. Codex and Antigravity workers have no such wait: for them the rule alone holds.

**How to apply:**
- Read the final note of every task that ends done: a promise to wait, or a run still going, is the sign. In its
  events (`djinn task watch <task>`), the line "Background tasks still running ... stopping them" proves it.
- Do not count its work done, nor build on it: continue the task when its worktree is still there, else spawn a task
  that runs it again, saying in its prompt to wait for the run in the foreground and report what it gave. A paid run
  run again costs again: say how much in the prompt or ask first when the cost is the developer's to decide.
- Since W80, a Claude task still RUNNING after its result, quiet, waits for its background work: it is not stuck.
  Stopping it stops the run.

See also [[regression-test-per-incident]].

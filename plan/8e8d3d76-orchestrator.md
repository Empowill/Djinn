---
id: 01a1184f-cf1b-7a9e-90f4-3c598e8d3d76
code: T07
phase: 2
status: open
---

# T07 · The orchestrator

**Goal.** Decide who does what, run each worker safely, and protect the machine.

## Decided
- **Wishes have a rank, set by hand** (drag and drop in the interface): when workers or gates
  are scarce, the scheduler serves the top wish first (T13).
- **One process per worker** (`claude -p`, `codex`, another CLI), started by Djinn. The
  existing Claude and Codex adapters are ported to Go, their fake-provider tests as the spec.
- **In a Git repository, one worktree per task**, created automatically and named after the
  task UUIDv7, in the project folder on Djinn's side. Branch names follow the project
  settings. Outside Git, writers run in parallel only when their write scopes do not overlap.
- **Scheduling**: task dependencies and exclusive write scopes, 1 to 16 workers, a freed slot
  reused at once.
- **Gates**: shared resources (code generation, a local stack, an end-to-end run) granted one
  at a time, according to measured load.
- **The machine**: per-worker CPU, memory, threads and processes (gopsutil on macOS and
  Linux); one cgroup per worker on Linux, created without root; pressure (PSI) to grant
  gates; pause and stop a worker in one gesture.

- **The machine is a resource**: discovered, measured tool by tool, and spent within its means;
  see T17.
- **What a worker may do** (Q34): in a project, the agent's own configuration for that project decides what the
  worker may do, and Djinn passes no permission setting that overrides it; a task outside any project (a wish
  without project) only reads, in an empty folder of its own under Djinn's data folder. Per provider:
  [`docs/providers.md`](../docs/providers.md).
- **Cross-agent permissions** (Q38): a project declares once what any agent may do in it, in
  `.agents/permissions.txtpb` (`djinn.v1.Permissions`: `edit`, `commands`, `denied_commands`, `network`, `mode`
  LISTED or AUTO), and Djinn translates it for the agent at launch, writing nothing in the project: Claude by
  `--settings` inline and `--permission-mode dontAsk|auto`, Codex by its sandbox, approval policy and reviewer, and
  by answering its approvals; agy by `--mode accept-edits` and `--sandbox`. What a translation loses is listed in
  `docs/providers.md`. Auto mode is the default of Djinn's own file, trusted as each provider documents it.
- **The order of decision**, recorded in `Task.access`: outside any project, read-only; the wish's allowance for
  the project (`djinn wish allow`, edit or auto, for that wish only); `.agents/permissions.txtpb`; the agent's own
  configuration in Git or in a folder holding an agent configuration file; otherwise, in a folder outside Git, the
  worker starts read-only and the task asks the developer whether it may edit (`Task.edit_question_id`, status
  `waiting` once the read-only worker ended). Only an explicit yes starts the worker again, allowed to edit and to
  run no command.
- **Every agent reads `AGENTS.md`**: Claude through its AGENTS.md plugin, set by Djinn in `--settings`, and the
  `CLAUDE.md` import; Codex and agy by themselves.

## Done when
- [x] `djinn task spawn` creates the worktree and runs a worker with a fake provider.
- [x] Two workers with overlapping scopes never run together.
- [x] A gate waits while the machine is under pressure, and says why.
- [ ] Djinn runs its own phase 3 tasks.

## Decided along the way
- **What is built** (`internal/harness`, `TaskService` in `api/plan/v1`): `spawn`, `list`, `get`, `stop`,
  `watch` (a server stream; the command line now prints a stream message by message, `--json` as JSON lines) and
  `clean`. `djinn up` serves them and stops every worker before closing the database.
- **`Provider` is a small Go interface**: `Start(ctx, Spec) Worker`; a `Worker` gives its `Events`, takes
  another message with `Send`, `Stop`s and `Wait`s. `Spec` already holds `Resume` and `Fork` (warm workers,
  Q28), unused. Four providers: `claude`, `codex`, `antigravity` and `fake`; how each is driven, what is verified
  and what is supposed, and the catalog of cases each fixture replays: [`docs/providers.md`](../docs/providers.md).
  Every real case met with a model becomes a fixture and a row there.
- **Claude**: `claude -p --input-format stream-json --output-format stream-json --verbose --permission-prompts
  none --session-id <task id>`, the prompt as a stream-json user message. Its input stays open for more
  messages and is closed once every message has its result, which ends the process. The `result` message gives
  the usage (`modelUsage` per model, `total_cost_usd`). Stderr lines are kept as `log` events. Claude and agy
  share one stream worker (`stream.go`).
- **Codex**: `codex app-server --listen stdio://`, JSON-RPC: a thread in the worktree, one turn per message, an
  approval asked declined. Tokens only, no cost. Ported from the Node runtime; no real stream captured yet.
- **Antigravity**: `agy --input-format stream-json --output-format stream-json`, one turn per message. Tokens
  only, no cost. Fixtures written from the documentation, none captured yet (T18).
- **A provider not installed** fails the spawn with "<command> not found in PATH", and the task records it.
- **Fake**: plays the prompt as a script (`text`, `tool`, `result`, `usage`, `sleep`, `fail`, `exit`) inside
  Djinn's process, for tests and to try Djinn without a paid model.
- **One process per worker**, in a process group of its own on Unix: stop sends SIGTERM to the group, then
  SIGKILL after 5 s. On Windows the process is killed at once; its children survive until a job object (T17).
  Djinn's environment is passed on, plus `DJINN_TASK_ID` and `DJINN_WISH_ID`; it is never read nor recorded.
- **Worktree**: `<data folder>/projects/<project id>/worktrees/<task id>`, branch `<code>-<slug>-<uuid8>` in
  lower case, from the project's `HEAD`; a project in a sub-folder of its repository runs in the same sub-folder
  of the worktree. Outside Git the worker runs in the project folder. The worktree stays when the task ends:
  `djinn task clean` removes it (refused with changes not committed, unless `--force`); the branch stays.
- **Task codes** `W1`, `W2`… per wish, like question codes. The task's project defaults to the wish's only one.
- **Events** (`TaskEvent`): one row per event, `seq` from 1 with no gap per task, a kind Djinn computes, and
  free `text` and `raw` (the provider's line as it came), each cut at 64 KiB. `watch --after-seq` resumes; the
  stream ends with the task.
- **Statuses**: `stopped` on request; `interrupted` when `djinn up` stops (SIGTERM, window closed) or crashed
  while the worker ran: at shutdown the workers are stopped and recorded so, and at start any task left running
  is. An interrupted task keeps its provider, session and worktree; nothing restarts it by itself.
- **The journal** gets the user's commands as received (`spawn`, `stop`, `clean`), and the harness's own changes
  under the actors `harness` and `worker` with the names `harness/start`, `harness/event`, `harness/end` and
  `harness/recover`: an event is journaled as the event itself.

- **Planned tasks** (`Task.depends_on`, `write_scopes`, `scheduled`, `wait_reason`): `djinn task spawn` takes
  `--depends-on` (a code of the wish, any case, or an id), `--write-scopes` and `--later`. A task that cannot start
  now is created `pending` with its reason, and an event `waiting: …` each time the reason changes; `djinn task
  watch` follows a planned task until it ends. `--later` only plans: the call never starts a worker. Without it, a
  task that can start starts in the call, as before, and a start error is the call's error.
- **The scheduler** (`internal/harness/schedule.go`) runs in `djinn up` (`Harness.Schedule`): one pass when a worker
  ends, a task is planned or stopped, and every 2 s for the pressure. It serves the tasks by the rank of their wish
  (`plan.ActiveWishes`), then the oldest. In order, a task waits for: its wish being active (a paused or granted
  wish keeps its tasks planned), its dependencies done, its write scopes free, the machine (no pressure, a slot
  free). Spawn and the pass decide under one lock, so a slot is never given twice.
- **A dependency that ends without being done** (failed, stopped, interrupted) fails its dependents, down the chain.
  A spawn on such a dependency is refused. A planned task is stopped at once by `djinn task stop`.
- **Access is decided when the worker starts**, not when the task is planned: an allowance given meanwhile counts.
- **Write scopes**: paths in the project's folder, cleaned, case ignored; none is the whole folder. Outside Git a
  task holds its scopes while it runs, and while it waits for the answer to its edit question (a yes restarts it
  to edit). In Git scopes are kept but not checked.
- **A task imported with a wish is not scheduled** on the importing machine (`scheduled` cleared on import).
- **Gates** (`internal/gate`, `GateService` in `api/machine/v1`): any name (`codegen`, `stack`, `e2e`, `paid`…), case
  ignored; one holder per gate, granted only when the machine is not under pressure, the first wish of the rank
  first, then the first come. A holder holds as long as its `Hold` stream is open: the gate goes back when the
  command ends, fails, is interrupted, or its process dies (the connection closes). `djinn gate run <name> --
  <command>` is the one command written by hand (`cmd/djinn/gate.go`): it runs the command on the caller's side,
  under the caller's own rights, never in Djinn's server. It returns the command's exit code. `djinn gate list`
  shows holders and waiters. With `$DJINN_TASK_ID` (set for every worker), the task's events show `gate <name>:
  waiting: …`, `taken`, `given back after 3s` (kind `GATE`).
- **What is not limited yet**: a worker started again by a yes to its edit question, and a gate held outside
  `djinn gate run`, bypass the slots.
- **Not built yet**: per-worker measures (gopsutil), cgroups, pause (T17).

## Open questions
- Branch names for workers: where does the team convention live? *Decided: in the project settings, default `<task-code>-<slug>-<uuid8>`.*
- Which permissions does a Claude worker get? *Decided (Q34): the project's own configuration; Djinn imposes
  none. Outside any project, read-only. Completed by Q38: `.agents/permissions.txtpb` first, translated per
  agent; a folder outside Git without configuration asks.*
- An event is stored twice: in `task_event` and in the journal. *Recommendation: keep it until a long Claude
  run shows the size matters; then journal the event without its raw line.*

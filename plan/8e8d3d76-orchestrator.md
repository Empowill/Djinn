---
id: 01a1184f-cf1b-7a9e-90f4-3c598e8d3d76
code: T07
phase: 2
status: in-progress
after: T02
---

# T07 · The orchestrator

**Goal.** Djinn decides, starts, resumes, integrates and pushes the workers' work by itself, in Go.

Decide who does what, run each worker safely, and protect the machine.

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
- [x] Djinn resumes its interrupted workers by itself, in the same task, and waits out a provider's usage limit.
  (`TestNothingLostOnShutdown`, `TestPausedInterrupted`, `TestResumeRules`, `TestSessionLimitWaitsThenResumes`,
  `TestSessionLimitBounded` in `internal/harness`; `TestCrashResumesTheWorkers` in `cmd/djinn` kills djinn up while a
  fake worker runs; `TestResumingFirst` in `internal/dispatch`; `TestResumingNotYourMove` in `internal/render`)
- [x] The scheduler's rules follow the automatic resume: an interrupted or resuming dependency holds its dependents,
  a task resumed as a fork holds no wish back, a worker holding or waiting for a gate is not paused.
  (`TestDependencyResumes` in `internal/dispatch`, `TestGo` in `internal/dispatch/bench`, `TestReady` in
  `internal/plan`, `TestPauseHoldingGate` in `internal/harness`, `TestOneAtATime` and `TestWaiting` in
  `internal/gate`)
- [x] After a restart the orchestrator alone puts the workers back, never the person: the resumed tasks start by the
  rank of their wish, then in their own order, before any planned task, a new spawn included; never more than the
  machine's slots and pressure allow, the others waiting with their reason; a paused wish's tasks wait for it; no
  worker starts twice. (`TestRestartQueue` in `internal/dispatch`: 6 resumed tasks over 2 wishes and 2 slots;
  `TestRestartResumesInOrder`, `TestRestartBeforeNewSpawn` in `internal/harness`)
- [x] A task is continued in place, never copied: `djinn task continue` gives a task no worker runs a new turn of its
  own session, in its worktree, through the scheduler; a fork of a task cut short closes it, continued in the fork.
  (`TestContinue`, `TestContinueWaitsForASlot`, `TestContinueRefused`, `TestForkClosesParent`, `TestContinueCommand`
  in `internal/harness`; `TestDependencyResumes` in `internal/dispatch`; `e2e/task-continue.spec.ts`: an interrupted
  task continued from the command line shows once in the Tasks tab)
- [x] Worker branch names follow the project's settings: `branch` in `.agents/settings.txtpb` or the developer's
  file, a template of `{code}`, `{slug}` and `{uuid8}` (required), checked by the proto when the file is read;
  default `{code}-{slug}-{uuid8}`; a planned task takes it when it starts. (`TestReadSettings`, `TestResolveSettings`,
  `TestProjectShow` in `internal/plan`; `TestBranchName`, `TestBranchFromSettings` in `internal/harness`;
  [`docs/team-settings.md`](../docs/team-settings.md#branch-names))
- [x] A yes to an edit question takes a slot like any task: on a full machine the task waits, resuming, with its
  reason, and starts once a slot frees. (`TestAskToEditWaitsForASlot`, `TestAskToEdit`, `TestAskToEditUnableToRead` in
  `internal/harness`)
- [x] A gate held outside `djinn gate run` weighs on the machine like one taken through it: held outside a running
  worker, it takes a slot (no new worker starts on it), it is measured when its holder's process is known, and it
  goes back once that process ends or its timeout passes. (`TestOutside`, `TestHolderEnds`, `TestTimeout` in
  `internal/gate`; `TestGateHeldOutside` in `internal/harness`: on a full machine a new worker waits, saying "1 worker
  runs and 1 gate is held outside the workers", and starts once the gate is given back)
- [x] On Linux with a user systemd, each worker runs in a cgroup of its own, made without root, which pausing, stopping
  and measuring take whole; elsewhere Djinn says so once and runs workers as before. (`TestScopeTakesTheTree` in
  `internal/harness` freezes, measures and stops a real scope whose child left its session, and skips without a user
  systemd; `TestProbeScopesFallback`, `TestReadCgroup`, `TestScopePrefix` in `internal/machine`;
  `TestWorkerScopesFallback` in `cmd/djinn`; T17 has the rest)
- [x] An azima whose work is done and whose plan file only waits for proofs no worker can give says so, apart from
  in progress and done, in the brief and the window. (`TestReadDoneWhen`, `TestProvers`, `TestAwaitingProof`,
  `TestBriefAzimas`, `TestSyncPlan`; `screens.test.mjs`: "an azima whose work is done awaits its proof")
- [x] The orchestrator reads the developer's answers: an answer ("Rub the lamp") starts one converter, "Q52 → tasks",
  that turns the decision into tasks linked to it, unless Djinn settles the question itself (an edit question, work
  that failed to integrate, a routed request, a grant); "Enlighten me" starts one investigator that revises the
  question; they take no slot but wait for the machine's pressure and memory, one per question at a time; a project
  setting or `djinn up --question-workers=false` turns them off; the lead is told when each starts and what it did when
  it ends; a worker's block, question and revision name its task. (`TestConverter`, `TestInvestigator`,
  `TestQuestionWorkersOff`, `TestOneConverterAtATime`, `TestQuestionEndLine`, `TestAnswerAFailedIntegration` in
  `internal/harness`; `TestQuestionWorkerTakesNoSlot` in `internal/dispatch`; `TestAnswerLine`,
  `TestWorkerAttribution`, `TestQuestionSettings`, `TestProjectShow` in `internal/plan`; `TestFromEnv` in
  `internal/cli`; `TestOnOff` in `cmd/djinn`; `e2e/question-workers.spec.ts`: "Rub the lamp" in the window, the
  converter ends with W2 spawned with `--decision Q01`)
- [x] The window pauses and resumes a worker from its card in the Tasks tab, any worker, not only a watcher: running, a
  pause button; paused, a resume button; none in a task no worker runs, and none on Windows, where the pause is refused.
  (`a running worker pauses from its card…` in `tests/screens.test.mjs`; `e2e/task-pause.spec.ts`: a fake worker
  paused from the window shows paused, then resumed)
- [x] A wish's branch keeps up with main by itself (Q55): the orchestrator fetches main at most hourly, and at once when
  the running Djinn finds a release, and merges it into the integration branch once main holds a release the branch
  lacks (or any commit, `merge_main`), the way a task's branch is merged, never a rebase; a code conflict or a red check
  start a correction worker. A Djinn built from a checkout watches the releases: on main, one that holds the build
  installs by itself (`install_releases`); on a branch, none installs over the build, main is merged instead.
  (`TestMergeMainAtARelease`, `TestLookAtMainAtOnce`, `TestMergeMainEachCommit`, `TestMainConflictStartsACorrection`,
  `TestMainAttemptsSpent`, `TestReleaseFit` in `internal/harness`; `TestMainSettings` in `internal/plan`;
  `TestALocalBuildFollowsItsCheckout`, `TestLocalBuild`, `TestReleaseSource` in `cmd/djinn`)
- [ ] Djinn runs its own phase 3 tasks. (needs: a lead that spawns phase 3 tasks with `djinn task spawn` on a real
  model, and a person who confirms it)

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
- **Worktree**: `<data folder>/projects/<project id>/worktrees/<task id>`, branch from the project's `branch`
  setting, by default `<code>-<slug>-<uuid8>` in lower case, from the project's `HEAD`; a project in a sub-folder of its repository runs in the same sub-folder
  of the worktree. Outside Git the worker runs in the project folder. The worktree stays when the task ends:
  `djinn task clean` removes it (refused with changes not committed, unless `--force`); the branch stays.
- **Task codes** `W1`, `W2`… per wish, like question codes. The task's project defaults to the wish's only one.
- **Events** (`TaskEvent`): one row per event, `seq` from 1 with no gap per task, a kind Djinn computes, and
  free `text` and `raw` (the provider's line as it came), each cut at 64 KiB. `watch --after-seq` resumes; the
  stream ends with the task.
- **Statuses**: `stopped` on request; `interrupted` when `djinn up` stops (SIGTERM, window closed) or crashed
  while the worker ran: at shutdown the workers are stopped and recorded so, and at start any task left running
  is. An interrupted task keeps its provider, session and worktree.
- **Djinn resumes its workers** (`internal/harness/resume.go`, 08/10): at start, after Recover, every interrupted task
  becomes `resuming` (`TASK_STATUS_RESUMING`, journaled `harness/resume`), unless a person stopped it, it was imported
  (`scheduled` false), its worktree is gone, its wish is granted, or another task forked from it (`fork_of`: it reads
  "resumed as W47"). The scheduler starts it first, in the same task, worktree and session (`--resume`, Codex's
  thread; agy from its first prompt), told "Djinn restarted while you worked; your worktree is as you left it.
  Continue your task.". A worker that fails on its provider's usage limit (`limit.go`: Claude's rejected
  `rate_limit_event` and "You've hit your session limit · resets 7:20am (Europe/Paris)", Codex's
  `usageLimitExceeded`, agy's `RESOURCE_EXHAUSTED`) leaves its task `resuming` until `Task.resume_after`: the reset
  plus a minute, else 15 minutes doubled at each resume, at most 2 h; meanwhile no new worker of that provider
  starts. `Task.resumes` counts; at 3, a task cut short again fails, saying so. A task stopped by a person never
  resumes. The window, the page and the brief show these tasks by status ("Resuming", "Waiting for the limit",
  "Resumed as W47"), never in what waits for the person. A task resumed as a fork no longer keeps its wish from being
  proposed for granting (`plan.Ready`): its fork does.
- **A task cut short never asks the person to start it again.** Djinn resumes every task it can by itself, so one
  left cut short (resumed as a fork, imported from another machine, its worktree gone) is history: among the finished
  tasks in the window, the page and the brief, never in what waits for the person, and it keeps no wish from being
  proposed for granting. What waits for the person is a question, an approval or a command to run, never a worker to
  restart. (`TestReady`, `TestBar`, `TestBriefFinished`, `tests/data-flight.test.mjs`, `e2e/review.spec.ts`)
- **The journal** gets the user's commands as received (`spawn`, `stop`, `clean`), and the harness's own changes
  under the actors `harness` and `worker` with the names `harness/start`, `harness/event`, `harness/end` and
  `harness/recover`: an event is journaled as the event itself.

- **Planned tasks** (`Task.depends_on`, `write_scopes`, `scheduled`, `wait_reason`): `djinn task spawn` takes
  `--after` (a code of the wish, any case, or an id; `--depends-on` before W113), `--write-scopes` and `--later`. A task that cannot start
  now is created `pending` with its reason, and an event `waiting: …` each time the reason changes; `djinn task
  watch` follows a planned task until it ends. `--later` only plans: the call never starts a worker. Without it, a
  task that can start starts in the call, as before, and a start error is the call's error.
- **The scheduler** (`internal/harness/schedule.go`) runs in `djinn up` (`Harness.Schedule`): one pass when a worker
  ends, a task is planned or stopped, and every 2 s for the pressure. It serves the tasks by the rank of their wish
  (`plan.ActiveWishes`), then the oldest. In order, a task waits for: its wish being active (a paused or granted
  wish keeps its tasks planned), its dependencies done, its write scopes free, the machine (no pressure, a slot
  free). Spawn and the pass decide under one lock, so a slot is never given twice. A spawn takes its turn in the
  pass (09/10): the tasks before it, the resumed ones first, take the free slots before it does, so a task spawned
  as a slot frees after a restart waits for the resumed ones.
- **A dependency that ends without being done** (failed, stopped by a person, resumed 3 times without finishing)
  fails its dependents, down the chain. A spawn on such a dependency is refused. An interrupted or resuming
  dependency holds its dependents: Djinn resumes it (W34's question, B); one resumed as a fork is its fork ("its
  dependency W1, resumed as W5, ended failed"). A planned task is stopped at once by `djinn task stop`.
- **Access is decided when the worker starts**, not when the task is planned: an allowance given meanwhile counts.
- **Write scopes**: paths in the project's folder, cleaned, case ignored; none is the whole folder. Outside Git a
  task holds its scopes while it runs, and while it waits for the answer to its edit question (a yes restarts it
  to edit). In Git scopes are kept but not checked.
- **A task imported with a wish is not scheduled** on the importing machine (`scheduled` cleared on import).
- **Gates** (`internal/gate`, `GateService` in `api/machine/v1`): any name (`codegen`, `stack`, `e2e`, `paid`…), case
  ignored; one holder per gate, granted only when the machine is not under pressure and holds the command's measured
  peak memory ([T14](8ce817da-cost.md)), the first wish of the rank first, then the first come. A holder holds as long as its `Hold` stream is open: the gate goes back when the
  command ends, fails, is interrupted, or its process dies (the connection closes). `djinn gate run <name> --
  <command>` is the one command written by hand (`cmd/djinn/gate.go`): it runs the command on the caller's side,
  under the caller's own rights, never in Djinn's server. It returns the command's exit code. `djinn gate list`
  shows holders and waiters. With `$DJINN_TASK_ID` (set for every worker), the task's events show `gate <name>:
  waiting: …`, `taken`, `given back after 3s` (kind `GATE`).
- **A yes to an edit question goes through the scheduler**: a task no worker runs (waiting for its answer, or failed
  while it read) becomes `resuming`, its wait reason "edit granted; …" (`answer.go`): it starts in the call when a
  slot is free, otherwise once its wish is active, its write scopes free and the machine has a slot and no pressure,
  on its session, told it may now edit, with its first prompt; no resume is spent. A yes while the read-only worker
  runs restarts it in its own slot. A task Djinn resumes already keeps waiting as it did, then resumes allowed to edit;
  one imported from another Djinn records the grant and starts no worker.
- **A gate held outside a running worker takes a slot** (`gate.Gates.Outside`, read by the scheduler through
  `Harness.GatesOutside`): held with no task, or for a task whose worker does not run (a person's terminal, a lead, a
  script, `djinn gate hold`), it counts as a worker in the capacity, and the wait reason says so ("2 workers run and 1
  gate is held outside the workers, the most this machine holds"). A running worker's gate, `djinn gate run`'s
  included, stays in the worker's own slot (`Harness.Works`). Giving one back wakes the scheduler (`Gates.Freed`).
  It is not refused on a full machine: it only keeps new workers from starting. Each gate held outside counts one
  slot, even two held by one process.
- **A holder's process and its timeout** (`GateServiceHoldRequest.pid` and `timeout_seconds`): `djinn gate run` gives
  its own process; a direct hold gives one with `--pid`. Djinn reads what that process and its descendants use every
  5 s while it holds the gate (`machine.ReadWorker`, Linux and macOS), shown in `djinn gate list` with its peaks
  (`Gate.resources`), and takes the gate back once it ends (signal 0 on Unix, `GetExitCodeProcess` on Windows). Every
  gate is taken back after its timeout, an hour by default and a day at most, so a forgotten hold never blocks Djinn;
  `Gate.expire_time` says when. Taken back, the stream says `GATE_STATE_TAKEN_BACK` with why ("its process 4242
  ended", "held past its timeout of 1h0m0s") and ends, the task's events say `gate <name>: taken back: …`, and
  `djinn gate run` prints it while its command goes on. A direct hold's use is shown, not recorded as a command's
  cost: only `djinn gate run` knows its command ended by itself. Without a pid, Djinn does not learn the holder's
  process from the connection (the Unix socket's peer credentials could give it).
- **Pause** (`djinn task pause <task>`, `djinn task resume <task>`, `TaskService.Pause` and `Resume`): on Linux and
  macOS, SIGSTOP then SIGCONT to the worker's process group (`process_unix.go`), or in a scope its cgroup frozen then
  thawed; the fake holds its script before its
  next step. The task is `paused` (`TASK_STATUS_PAUSED`, an event `paused: …` then `resumed`, journaled
  `harness/hold`), and its worker takes no slot (`Harness.Running`): a planned task may start meanwhile. Resuming
  does not wait for a slot: the developer asked for it. A paused task keeps its write scopes. Stop sends SIGTERM then
  SIGCONT, so a paused worker stops at once; `djinn up` stopping or crashing interrupts it like a running one. What
  the worker wrote just before the pause may still land after the `paused` event. On Windows the call refuses, saying
  why: no signal stops a process tree there; it would take suspending each thread, or a job object (T17).
- **The window pauses too** (W49's question, yes): a button on every running or paused task's card, a worker or a
  watcher, calls `Pause` or `Resume`; a refusal (a gate held or awaited, a worker that cannot pause) shows as a toast.
  The window hides it when `Machine.os` is windows (`usePausable` in `src/data/djinn.tsx`). The wish's own Pause, in its
  head, stays as it is: it sets the whole wish aside.
- **A worker that holds a gate is not paused** (W49's question, A): the gate would stay held, frozen, for every other
  worker. The pause is refused, saying "W1 holds the gate test: wait or stop it" (`gate.Gates.Held`, given to the
  harness by `djinn up`).
- **Nor is a worker that waits for a gate** (W60's question, A): paused, it would get the gate in its turn and hold
  it frozen. The pause is refused, saying "W1 waits for the gate test: wait or stop it" (`gate.Gates.Waiting`, given
  with `Held`). A worker that holds one gate and waits for another is told it holds.
- **What a task waits for is set again as the plan learns** (`djinn task depend`, `TaskService.Depend`): its
  dependencies, in place of those it had, tasks of its wish by code; the tasks of a wish form a graph without cycle,
  and a dependency that would close one is refused, naming it ("W1 → W3 → W1"). A task whose worker runs is refused
  until it ends: its worker writes the task as it goes. (`TestDepend`)
- **A task gets its place at its spawn, never after** (W113, 10/10): spawning N, then depending W5 on it, left a gap in
  which a pass could start W5. `djinn task spawn --after W1,W2` (`TaskServiceSpawnRequest.after`; `depends_on` kept,
  read as a synonym, so that nothing breaks) gives a task what comes before it; `--blocks W5` adds the new task to
  W5's dependencies under the same `h.sched` lock and in the same transaction as the spawn, checked again inside it.
  It is refused for a task that has started, saying where it stands ("task W5 has started (running)"), and for one
  that would close a cycle ("W1 → the new task → W3 → W1"), through `part_of` too; an azima can block as work does.
  `djinn task depend` sets several tasks at once (`--after` for the task, `--also W6=W5,W3` for each other one, a
  `TaskAfter` the command line reads as `key=a,b`), all or none in one transaction under `h.sched`, the resulting graph
  checked for cycles. Every list of tasks takes commas: `--after W1,W2`. The brief and `docs/agent-protocol.md` tell
  the lead: `--after` at spawn, `--blocks` to insert before, never spawn then depend. (`TestSpawnBlocksWhilePassesRun`,
  scheduler passes in a tight loop; `TestSpawnAfterAndBlocks`; `TestDependSeveral`)
- **Azimas: the plan is a graph Djinn understands** (10/10; an azima, Arabic ʿazīma, the incantation that binds and
  commands a djinn). `Task.kind` is `WORK` (the default: a worker runs it) or `AZIMA` (`T07`: no worker ever, never
  scheduled, never waiting for the person, never in the moving or waiting work). Work is part of an azima
  (`Task.part_of`, `djinn task spawn --part-of T07`, changed by `djinn task group <task> --part-of T07`), a grouping
  and never a wait: only `depends_on` makes a task wait, so a part runs while its azima still waits for others
  (`TestAzimasAreNeverScheduled`). A task that depends on an azima waits "for the azima T1 to be done". The tasks of a
  wish, through `depends_on` and `part_of` together, form a graph without cycle: `Depend` and `Group` refuse one,
  naming it ("T1 → W1 → T1", `TestGroupRefusesCycles`). Where an azima stands is computed on every read, never stored
  (`Task.azima`, `plan.FillAzimas`): done when marked done (`djinn task done`) or its plan file says so, in progress
  when a part has a worker on it or is done, open otherwise, ready when every task it depends on is done; with its
  parts, done and running. `djinn task spawn --kind azima` makes one (T1, T2… after the highest).
- **`djinn plan sync <wish>`** (`PlanService.Sync`, `internal/harness/azimas.go`) reads the `plan/*.md` of the wish's
  projects (front matter `id`, `code`, `phase`, `status`, and the title) into its azimas: found by the file's id, else
  its code; the missing ones made with the file's id; title, phase and file updated; never what they depend on. A file
  whose status is `done` closes its azima (`Closure.actor` `PLAN_FILE`), and opens it again once it says otherwise.
  Then it writes each azima's dependencies back into its front matter, `after: T08 T17` after `status`, so that Git
  carries the graph; the store stays the source of truth, and an azima made by a sync takes its file's `after` once
  (`TestSyncPlan`, on a copy of this folder). The T01…T24 a plan import stored as work, never run, become azimas at
  the next `djinn up` (`migrateAzimas`, journaled `harness/azima`, `TestMigrateAzimas`).
- **The lead plans with them**: the brief shows the azimas as a graph, the ready ones first (under way before open),
  then the blocked ones with what they wait for, the done ones on one line (`TestBriefAzimas`), and its rules say to
  spawn work `--part-of` its azima and `--after` only what it needs. The Tasks tab groups work under its azima,
  which shows what it waits for and its progress (`screens.test.mjs`, `e2e/azimas.spec.ts`); the page and the flight
  plan never list an azima as work or as waiting for the person.
- **An azima whose work is done awaits its proof** (W124: T01, T03, T04… read "in progress" with nothing left for
  Djinn to do). `AzimaState` `AWAITING_PROOF`, between in progress and done: under way, every work task part of it
  finished (done, stopped, or cut short for good, `plan.Finished`), every azima part of it done or awaiting too, and
  every unchecked Done-when box of its plan file saying `(needs: …)` or holding boxes that do (`plan.FillAzimas`,
  `TestAwaitingProof`). `djinn plan sync` reads the boxes strictly (`plan.ReadDoneWhen`): top-level and nested boxes
  over several lines, a struck box (`~~…~~`) left out, a section without boxes left, a file without the section (T09)
  giving none, needs only in a parenthesis that opens on them (T16's "(Go side done…; needs: …)" stays work); it keeps
  them on the azima, box by box (`Task.proof_needs`: the box's text, its needs, who gives the proof). Who gives it is
  read from the plain words of the needs, no model (`plan.Provers`): a Mac, a Windows machine, a review by a named
  person ("Clément's review"), a release, a real model, a person, else the needs' own words (`TestReadDoneWhen`,
  `TestProvers`). A file whose boxes are all checked closes its azima whatever its status line says, and the sync
  reports it (`all_checked`, T17's case; `TestSyncPlan`). The brief lists these azimas apart, "Work done, waiting for
  its proof: T11 needs a Windows machine, Clément's review" (`TestBriefAzimas`), and tells the lead to spawn no work
  for them. The window gives them their own tone and label ("Proof awaited", "Preuve attendue"), what they need on
  hover and in their card; the wish's head counts them apart ("3 done · 18 awaiting proof / 30 azimas"); the flight
  plan lists the proofs a person can give (needs that name a person or a review) under "Proofs you can give", among
  what waits for you, never as work (`screens.test.mjs`, `data-flight.test.mjs`).
- **A follow-up continues the task, it does not copy it** (W83: every resume through a fork left a duplicate):
  `djinn task continue <task> --prompt "…"` (`TaskService.Continue`) makes a done, failed, stopped or interrupted task
  `resuming` again, `Task.continuing` set: the scheduler starts it like any task (slots, dependencies, pressure, the
  resumed ones first), on its own session with the new prompt, in its worktree and branch. Its events say "continued
  by the lead, was …: <the prompt's first line>", the prompt, then "continued: started …, resuming its session". Its
  usage sums, its automatic resumes count again from none. Refused while it runs, waits for its edit question or has
  not started; when it continues in a fork; when its worktree is gone (fork it instead); for Antigravity and a watcher,
  which resume no session, a task without session, one imported, one whose budget is spent, or one of a granted wish.
- **A fork of a task cut short continues it**: `djinn task spawn --fork W55` on an interrupted, resuming, failed or
  stopped task closes it in the same transaction, done, `Task.closed` with the lead, the note "continued in W57" and
  `Closure.continued_in`, which the scheduler follows: a task that depends on W55 waits for W57. The Tasks tab shows
  it finished, with W57 as a link to its card. A fork of a running or done task leaves it as it is. The lead's brief
  says it: to follow up on a task, continue it; fork only to start a different task from its context.
- **Per-worker measures** are built, without gopsutil: each worker's process group, read from /proc on Linux and
  `ps` on macOS, its latest reading and peaks on its task (T17).
- **A cgroup per worker on Linux** (T17): where the user has a systemd, each worker runs in a systemd user scope of
  its own, `djinn-<task code>-<uuid8>`, made without root by `systemd-run --user --scope`, the `--worker-cpu` cap and
  the memory ceiling as its properties. Pausing freezes its cgroup, stopping signals and then kills every process in
  it, measuring reads its files: a tool that left the worker's process group goes with it. Without a user systemd, or
  on macOS and Windows, `djinn up` says so once and the process group serves, as before.
- **Question workers** (Q52, A; `internal/harness/question.go`, W86 on 09/10, ported onto azimas, integration and
  the memory rule by W133 on 10/10): the lead is no longer the bottleneck of an answer. An answer
  (`QuestionService.Answer`, or an approval) starts a converter (`Task.role` CONVERTER, `Task.question` Q52, title
  "Q52 → tasks"), unless it settles itself (an edit question, work that failed to integrate, a routed request, a grant);
  "Enlighten me" starts an investigator (INVESTIGATOR, "Q52: enlighten"). The prompt holds the question, its options,
  context and recommendation, the answer and note (or what to dig into), the moving part of the brief, and one job.
  Access `TASK_ACCESS_DJINN`: reading, plus `djinn` commands to read the wish and to spawn, ask or revise, and Git's
  reading ones, `tilasm get` included; no edit, no gate, no worktree (the project's folder), never integrated. It takes
  no slot (like a watcher: it waits on the network, builds nothing), but it is an agent: it waits under pressure and
  until the memory holds its provider's typical peak, which then counts for the tasks after it
  (`dispatch.QuestionWorker`). Djinn spawns it as it spawns a correction worker (`harness.spawn`, journaled as the
  harness's). Its prompt plans with `--part-of` and `--after`. One per question and role while it works: a
  second answer is sent to it. An answered question is a decision and is not revised: a converter asks a new question
  for what the answer leaves open. The lead's line says "W12 turns it into tasks"; at its end, `questionEndLine` says
  what it spawned (tasks with `decision` Q52 created since), asked (`Question.task_id`) or revised (`Round.task_id`),
  and what is left. Settings `question_workers`, `question_model` (default `sonnet` for claude), `question_budget_usd`
  (default $2) in `.agents/settings.txtpb`; `djinn up --question-workers=false` or `DJINN_QUESTION_WORKERS=off`. The
  harness starts none unless `djinn up` asks (`WithQuestionWorkers`): tests never reach a model by an answer.
- **Attribution by the environment** (W59's question, B): a request field marked `(djinn.v1.env) = "DJINN_TASK_ID"`
  takes the variable when the command line or `djinn mcp` leaves it empty: `djinn block put`, `djinn question ask` and
  `revise`. The decision log says "By W12", the page "Revised by W12". The e2e specs drop the variable: their djinn is
  not the worker's.
- **Main merged, never rebased** (Q55, A): a wish's integration branch takes main's commits by a merge in the
  integration worktree (`harness.mainPass`, `main.go`), deterministic, under the same rules as a task's merge
  (`moveBranch`, `commitChecks`, `settleGenerated` are shared). A merge costs no model and keeps the history pushed: a
  rebase would rewrite the branch and need a forced push, which Djinn never does. A model only settles a conflict: a
  correction worker whose `IntegrationFailure.main_sha` says it merges main; its success records the merge
  (`settleMain`). Past `correction_attempts`, that main's commit is left (`WishMain.failed_sha`) until main moves.
  Releases are tags `v*` whose commit, or its parent (the release commit holding `dist/`, off main), is in main; the
  release commit itself never comes in. A build from a checkout (`local-<commit>`) follows the GitHub releases of the
  variant it would have been; `harness.Release` says what one means from where the checkout stands.

## Open questions
- Branch names for workers: where does the team convention live? *Decided: in the project settings, default `<task-code>-<slug>-<uuid8>`; built (`branch`, `TestBranchFromSettings`).*
- Which permissions does a Claude worker get? *Decided (Q34): the project's own configuration; Djinn imposes
  none. Outside any project, read-only. Completed by Q38: `.agents/permissions.txtpb` first, translated per
  agent; a folder outside Git without configuration asks.*
- An event is stored twice: in `task_event` and in the journal. *Recommendation: keep it until a long Claude
  run shows the size matters; then journal the event without its raw line.* 10/10/2026, it does (the data model,
  tilasm L02): on the developer's database, read only, `harness/event` is 121 MB of a 123 MB journal, beside 122 MB
  of events, in a 279 MB file; no code reads a `harness/*` entry back. To decide: journal it as `(task_id, seq)`
  only, or without its raw line, or not at all; old entries stay. Smaller, same tilasm: `TaskEvent.raw` is read only
  by `djinn task watch --raw`; `InboxItem.settle_time` is written, never read; `Mark.actor` and `Round.actor` are
  always `local`, never read.
- Claude ignores a worktree's own `.claude/settings.json` while the worktree is not trusted ("this workspace has not
  been trusted"): seen on 08/10/2026 with the first workers Djinn started, its 21 rules lost, `.agents/` still applied
  through `--settings`. `Claude.args` and Q34 say the project's own configuration decides, so a project without
  `.agents/permissions.txtpb` would lose its rules. Have Djinn's worktrees trusted, or rely on `--settings` alone and
  say the loss in `docs/providers.md`? Not checked again since. (needs: a real claude run in a worktree)

## From T30 · Djinn integrates finished work by itself

**Goal.** A worker's work counts once it is in the wish's branch, tested, not when its worker ends. Djinn brings it
there by itself: the lead no longer merges, regenerates, tests and installs by hand, and the person is asked only
what is theirs to decide.

**The developer's words.** "Why is it you who coordinates, merges, runs the tests and all, when the Go orchestrator
is supposed to do all that?" Today Djinn schedules, runs, resumes, gates and measures the workers; then each one's
work sits on its branch, and the lead integrates it by hand (`git merge`, a generated file regenerated, the tests
through a gate). It costs: nothing moves while the lead is away (W107–W110 waited through a session limit), a task
that waits for another starts on a branch without its work (W112 and W113 started before W111 was merged), and the
lead spends its tokens on mechanical work.

### What is decided

- **The deterministic part is Go, no model.** When a worker ends done, Djinn merges its branch into the wish's
  integration branch (`feat/wails-go` here: a setting of the wish in its project), in a worktree of its own, never in
  the person's checkout. A conflict only in generated files (a project setting: their paths, and the command that
  makes them, `go tool task gen` here) is settled by making them again. Then the project's test command (a project
  setting) runs through a gate; green, the integration branch moves on. The person's checkout of that branch, when
  clean and behind, follows by a fast-forward; otherwise Djinn says so and leaves it.
- **Judgement goes to a worker, not to the lead.** A conflict in code, or red tests, starts a worker of correction on
  the merge, with what failed, part of the same azima; its work integrates the same way. After a few attempts
  (a setting), Djinn asks the person a question.
- **Commit at once, push on a cadence** (the developer, 09–10/10/2026). Each task's work is merged and tested into the
  integration branch as soon as the task ends, alone: its dependents build on it at once. Pushing that branch to its
  remote is the orchestrator's, never an agent's (agents keep `git push` denied, `.agents/permissions.txtpb`).
  Djinn pushes **automatically by default**, checked each time a task's merge ends: when an azima ends (its last
  part is committed), or when at least **three tasks are committed locally and more than an hour has passed since
  the last push**. The count, the hour and the mode are settings of the wish: `auto` (the default) or `ask`, where a
  question "Push feat/wails-go to origin? (3 commits: …)" lets the person push in one click (Rub the lamp). A push
  refused by the remote (behind, protected) never forces: Djinn says why, and asks.
- **A task done without its work committed is not done.** W94 ended "done" with its whole change staged in its
  worktree and no commit: integration finds a branch with nothing new, or a worktree with changes, and says so (the
  task is not done: its work waits, uncommitted), rather than counting it. Nor does Djinn commit it blindly (the
  developer, 10/10/2026: debug files, abandoned attempts or artifacts would go in too): a review worker judges it.
- **A task waits for its dependencies to be integrated**, not only done, and its worktree starts from the integration
  branch, so it builds on their work.
- **The person decides what is theirs**: to install and restart on the new build (Djinn proposes it, with what
  changed and what to check), and the choices no worker can make.

### Done when

- [x] A task done integrates by itself: merged in its own worktree, generated files made again, tests through a gate,
  the branch moved on, the person's clean checkout fast-forwarded; each step in the task's events and status.
  (TestIntegrateADoneWorker, TestCommitEachTaskAlone, TestIntegrateGeneratedConflict, TestIntegrateCodeConflict,
  TestIntegrateRedTests, TestIntegrateLeavesADirtyCheckout, TestIntegrateFollowsACleanedCheckout,
  TestIntegrateABranchNoCheckoutHolds, TestRecoverAnIntegration)
- [x] Each task committed alone as it ends; the push automatic at an azima's end, or with three tasks committed and an
  hour since the last push, checked as each merge ends; `ask` mode by a question; a refused push never forced. Each
  push in the journal and on the wish (its head shows the last one); the build proposed after a push; a task's
  worktree removed once its work is committed, kept with changes not committed. Fake clock, bare remote in a temporary
  folder. (TestCommitEachTaskAlone, TestPushAtAnAzimasEnd, TestPushAfterThreeTasksAndAnHour, TestPushDue,
  TestCommittedSince, TestPushAskMode, TestARefusedPushIsNotForced, TestNoRemoteNoPush, TestABuildIsProposed,
  TestRemoveTheWorktreeOnceCommitted, the screens test "the wish's head says where Djinn last pushed its integration
  branch, and a push refused")
- [x] A code conflict or red tests start a correction worker, part of the same azima; after N attempts, a question.
  (TestCorrectACodeConflict, TestCorrectRedTests, TestCorrectionAttemptsThenAQuestion,
  TestAnswerAFailedIntegration, TestACorrectionWorkerThatFails)
- [x] Work not committed is never merged nor committed blindly: before a task's merge, a correction worker's
  included, Djinn reads its worktree (`git status --porcelain`, untracked files included, `.gitignore` applying);
  with changes, a review worker starts in that worktree, part of the same azima, of the task's provider, the task's
  title and prompt, the files and their diff in its first prompt; it commits what belongs to the task and drops the
  rest, and its work integrates like any task's ("uncommitted: reviewed by W5"); past the attempts, a question.
  (TestIntegrateACleanWorktree, TestReviewUncommittedWork, TestReviewAttemptsThenAQuestion,
  TestReviewACorrectionsWork, TestAReviewThatFailsAfterItsCommit)
- [x] A task waits for its dependencies to be integrated, and starts from the integration branch.
  (TestWaitsForTheCommit, TestADependentStartsFromTheCommit)
- [x] The window, the page and the brief show where each task's work stands (done, integrating, integrated,
  conflict, red), and propose installing once a batch is integrated. (TestWorkStands, TestBriefWorkStands, TestRun
  "where a task's work stands", the screens tests "the Tasks tab says where each task's work stands" and "the update
  banner proposes to install a build committed", TestABuildIsProposed, TestUpdateInstallsABuild)

- [x] Each project says what Djinn checks (W141): `setup`, the command that makes a fresh integration worktree ready,
  run once and again when its lock files change; `checks`, each with its gate and when it runs, before a commit (red,
  a correction worker) or before a push (red, the push held and said, a question if it stays red); the former `test`
  a check at commit. The brief and each worker's first prompt say them; `djinn project show` and the project view
  show them with their last runs. Djinn's own: `npm ci`, lint at commit, test at push. (TestSetupOncePerWorktree,
  TestSetupAgain, TestARedCommitCheck, TestPushChecksHoldThePush, TestPushWithoutTheChecks, TestReadSettings,
  TestResolveSettings, TestProjectShow, TestChecksBrief, TestDjinnsOwnSettings, TestBrief, the screens tests "the
  project view lists the setup and the checks, when each runs, and how each last ran" and "the wish's head says a push
  its checks hold, why on hover")

### Merged by hand into feat/wails-go

Until Djinn integrates by itself, the lead merges finished branches. W129 (10/10/2026) brought four branches onto
W115–W122, in this order, each merge tested green (`go tool task lint`, `go tool task test`) before the next:

- **W123**, built on W121 beside W122: both kept. `ProjectSettings.install` took 9 (`correction_attempts` holds 8).
  Its `TaskIntegration.corrected_by` is W122's (7): a task's id, which the page, the brief and the window show by its
  code. A correction worker's worktree starts from its failure's base; any other from the wish's integration branch.
- **W124**: `Task.proof_needs` took 43 (40–42 held by integration, tilasms, correction). The brief lists the azimas
  awaiting their proof with the tilasms that explain them.
- **W125**: `Wish.description` took 15 (12–14 held by the integration branches and the commit cadence). The brief
  keeps its order, the wish before the rules, with the tilasms after what runs and waits.
- **W127**: golangci-lint and ESLint now check the code W116–W125 brought too: their findings fixed or, where the code
  means it, left with a reason.

The method counts of `internal/cli`'s tests, which broke at every merge, are now derived from the protos: every public
method is a command line with its help, and every one that answers once is an MCP tool with its comment
(TestEveryPublicMethodIsExpressible, TestMCPListTools).

### Tests must be fast

No real sleep, fake clocks, milliseconds: a test over 1 s is a bug. Merges and conflicts on a real Git repository in
a temporary folder, with a fake test command.

## From T16 · Dispatch: plain Go code or a local model?

**Goal.** Decide, on numbers, whether dispatching needs a model at all. Dispatch only: which ready
task starts now, on which worker, once its dependencies, write scopes and gates allow it. Deciding
*what* the tasks are, or recommending sub-agents, stays with the large model that plans the wish.

### Depends on
- **T17, knowing the machine.** Whether a local model can run at all comes from the discovery
  (a usable GPU and its drivers, the memory), and a dispatcher must respect the machine's budget
  whatever it is made of. (The verdict exists: `can_run_local_model` and its reason, in `djinn machine show`)

### The contenders
- **Plain Go code**: the scheduler of the orchestrator (T07). Deterministic, free, instant.
  Preferred: if it covers the cases, no model is needed.
- **A local open-weight model**: Gemma 4 (Apache-2.0, native tool calling), run by Ollama on the
  machine, given the same inputs and asked for the same decision.

- **Consensus across machines** (Raft and the like) is a different question: see T15.

### The benchmark
- A fixed set of dispatch situations, as data: task graphs, overlapping write scopes, busy gates,
  machine pressure, a worker that failed, a question that blocks.
- For each, the expected decision, written by hand.
- Measured for each contender: decisions right, decisions wrong, time per decision, CPU and memory
  used on a laptop without a GPU and on an Apple Silicon Mac.
- The model gets a case only where the Go code has no rule; if there is none, the model has no role.

#### The Go side (done)
- The scheduler's rules are a pure function, `internal/dispatch`: a `Situation` (tasks, wishes, projects in Git,
  the machine's slots, running workers and pressure) and a `Decision` per planned task (start, wait with its reason,
  fail). The harness acts on it; nothing else changed in what it decides.
- The cases: `internal/dispatch/bench/cases.json`, 35 situations written by hand with the expected decision of each
  planned or resuming task: dependencies (running, done, chains, failed, stopped, interrupted or resuming and so
  waited for, resumed as a fork that runs, is done or failed, gone, waiting for a question),
  write scopes (outside Git, in Git, the whole folder, case, another project, two planned writers), the machine
  (pressure, full, one slot for two), wish ranks (rank, then age, unranked, paused, granted), a busy gate. JSON, so
  that a model reads the same file.
- `go tool task bench-dispatch` prints the table. `TestGo` checks that Go decides every case as expected.
- First run (linux/amd64, 16 cores): 31 of 31 cases right, 42 of 42 decisions, about 7 µs per pass, the
  situation built included.
- After the automatic resume (W60): an interrupted or resuming dependency is waited for, and a dependency resumed as
  a fork is its fork. 35 of 35 cases right, 47 of 47 decisions, about 4 µs per pass.
- What the cases show: Go has a rule for each of them. Gates are not a dispatch decision: a worker waits for its gate
  when it runs the command, so a busy gate holds no task. A question blocks only through its task's state (waiting).

### Done when
- [ ] The benchmark runs with one task command and prints a table. (Go side done: `go tool task bench-dispatch`,
  `TestGo` in `internal/dispatch/bench`; needs: Ollama with Gemma 4 on the machine, and a decision to download it,
  for the model side)
- [ ] A decision is written down: Go only, or Go plus a model for named cases, with the numbers. (needs: the
  bench, then a person to decide)

---
id: 01a11879-2612-7d67-ad37-0a708ce817da
code: T14
phase: 2
status: in-progress
after: T02 T07 T13
---

# T14 · Fast, economical workers

**Goal.** Workers start fast, with the right context and the right model, as many as the machine holds.

Delegating to Djinn costs less than doing the same work in one big session: large
models do the work that needs them (planning, hard code, review), and everything else runs on
code, cheaper models, or a local model.

## Ideas, cheapest first
- **Dispatching is code, not a model.** Scheduling, write scopes, dependencies and gates are
  deterministic: they cost no token.
- **One model per task, chosen in the task file.** A large model to plan and review; a smaller,
  cheaper one for mechanical changes.
- **Smaller prompts.** A worker gets the project index and its task file, not the whole
  conversation. Stable prefixes (`AGENTS.md`, the index) benefit from prompt caching.
- **Resume instead of restart.** Today each pass starts a new provider process and rebuilds its
  context; resuming a session keeps the cache warm.
- **A local open-weight model, for dispatch only, and only if plain code cannot do it**: see T16,
  which benchmarks Gemma 4 against the Go scheduler.
- **Measure cost per task and per worker** (tokens and money), so every choice above is checked
  against numbers.

## Done when
- [x] Each task records the tokens and the cost it used. (08/10: a fake task that reports `usage 1200 300 0.02`
  lists `usage: input_tokens 1200, output_tokens 300, cost_usd 0.02`; Claude's result is read by `TestParseClaude`;
  Codex and agy give tokens only)

## From T17 · Know the machine, spend it wisely

**Goal.** The machine is a resource like any other. Djinn knows what it has, learns what each
tool costs, and decides how many workers to start and what they may run, so the machine is never
overloaded. Part of the orchestrator (T07).

### What to do
- **Discover the machine** at start-up: CPU cores, memory, disk space, and the GPU with its
  drivers (NVIDIA and CUDA, Apple Silicon and Metal, an integrated GPU or none). Stored as a
  `Machine` record, refreshed when it changes.
- **Measure the tools.** When a worker runs a project command (`go build`, `npm test`, a code
  generator, an end-to-end run), Djinn records which command ran and what it took: CPU time, peak
  memory, duration, through the worker's cgroup (Linux), its process group (macOS) or its Job
  Object (Windows). Over time each project has a profile of what its usual commands cost.
- **Compute the capacity**: how many workers can run now, and which commands may start, from the
  machine, the live pressure and the profiles. A heavy command waits for a gate; a light one
  starts.
- **Answer "can this machine run a local model?"** from the discovery: no usable GPU and little
  memory means no (T16 depends on this).

### Done when
- [x] `djinn machine` shows the discovered machine and the live load.
- [x] After a few runs, each project command has a measured cost. (for the commands run through `djinn gate run`,
  on Linux: `TestRunCost` in `internal/gate`, `TestCosts` in `internal/machine`; by hand, `djinn command list` after
  three runs in a temporary data folder showed `sh -c exit 3`, 2 runs, and a `sort` of 300 MB at 0.64 s of CPU and
  302 MB of peak memory. A command an agent runs with its own tool, outside a gate, is not measured)
- [x] Djinn never starts more workers than the machine holds, and says why it waits. (after a restart too:
  `TestRestartQueue` in `internal/dispatch`, `TestRestartResumesInOrder` and `TestRestartBeforeNewSpawn` in
  `internal/harness`)
- [x] `djinn machine show` gives the disk, the GPUs and their drivers, and whether a local model can run, with why.
  (`TestReadGPUs`, `TestLocalModel` and `TestReadThisMachine` in `internal/machine`; by hand, `djinn machine show` on
  this Linux laptop gave its disk, an Intel GPU on i915, and "on the CPU, slowly, with 31.0 GiB of memory". Not run
  on a Mac nor on an NVIDIA or AMD machine yet: their fixtures are what proves them)
- [x] Djinn reads what each worker uses while it runs, keeps the latest reading and the peaks on its task, and shows
  them in `djinn task get`, the Tasks tab and `djinn machine show`. (`TestReadGroup` on a simulated /proc,
  `TestParsePS`, `TestReadWorker` and `TestNotMeasured` in `internal/machine`; `TestMeasureWorker`, a fake claude read
  from /proc on Linux, `TestMeasurePeaks`, `TestNotMeasured` and `TestWorth` in `internal/harness`; "a running task
  shows what its worker uses now" in `tests/screens.test.mjs`. macOS reads `ps`: not run on a Mac yet. Windows says it
  is not measured yet)
- [x] A gate goes to a command only when the machine holds what it was measured at, and says why it waits; a light
  command goes at once, one never measured as it comes. (`TestMemory` in `internal/gate`, with a fake machine and fake
  costs: `go tool task e2e` at 3.1 GiB waits with 1.8 GiB free, then goes once 8 GiB are; a second heavy command waits
  while it holds its gate. `TestRoom` and `TestCosts` in `internal/machine`)
- [x] Another worker starts only when the free memory holds the typical peak of a worker of its provider, and the
  task says why it waits, with those numbers; a resumed task keeps its turn. (`TestMemoryHoldsWorker` in
  `internal/dispatch`: 2.2 GiB free holds a resumed claude worker typically at 1.5 GiB, and a lighter codex one
  behind it, then 3 GiB starts the first; `TestMemoryHoldsWorker` in `internal/harness`, a fake machine at 2 GiB
  holds the second worker, at 3 GiB starts it, the first still running; `TestTypical` and `TestWorkerRoom` in
  `internal/machine`; `TestRestartQueue` in `internal/dispatch` unchanged)
- [x] On Linux with a user systemd, each worker runs in a systemd user scope of its own, which measuring, pausing and
  stopping take whole, even a process that left its group; a memory ceiling per worker can be set; without a user
  systemd, or off Linux, `djinn up` says so once and workers run as before. (`TestScopePrefix`, `TestReadProbe`,
  `TestCheckQuota`, `TestCheckMemoryMax`, `TestReadCgroup` on fixture cgroup files, `TestCgroupFiles`,
  `TestProbeScopesFallback` with no systemd-run then a fake one on PATH, and `TestProbeScopes` in `internal/machine`;
  `TestScopePrefix`, `TestMeasurePeaks` and `TestScopeTakesTheTree` in `internal/harness`, which on a machine with a
  user systemd measures, freezes and stops a real scope whose child left its session, and reads its 512 MiB
  `memory.max`, and skips elsewhere, as in CI; `TestWorkerScopesFallback` in `cmd/djinn`. On this Ubuntu 22.04 laptop
  the probe made its scope and said the cpu controller is not delegated. A worker killed at its memory ceiling is not
  tried yet)

### Decided along the way
- **The minimum first** (`internal/machine`): cores (`runtime.NumCPU`), memory, load and pressure, read live (at
  most once a second), not stored yet. Linux: `/proc/meminfo`, `/proc/loadavg`, `/proc/pressure/{cpu,memory}` when
  the kernel has PSI. macOS: sysctl (`hw.memsize`, free pages, `vm.loadavg`, and the system's own memory pressure
  level). Windows: cores and memory (`GlobalMemoryStatusEx`), no load nor pressure. Elsewhere: the cores. `djinn machine show` prints it, with the workers it holds and why.
- **The disk and the GPUs.** The disk is the data folder's, where the worktrees live: `statfs` (Linux, macOS),
  `GetDiskFreeSpaceEx` (Windows), read with the rest. The GPUs are read once, at start-up, in the background; not
  refreshed while Djinn runs. Linux: one per DRM card of `/sys/class/drm` on the PCI bus (`device/uevent`: vendor,
  driver), the driver's version from `/sys/module/<driver>/version`, amdgpu's memory from `mem_info_vram_total`;
  `nvidia-smi` names NVIDIA's and gives their memory, run only when a card is NVIDIA's or `/sys` shows none (WSL).
  Apple Silicon: one GPU, the chip's name, Metal, the machine's unified memory. An Intel Mac and Windows: none read
  yet (`system_profiler` takes seconds). No CUDA version: the driver's says which it supports.
- **Can it run a local model?** (`Policy.LocalModel`, every threshold a field): no with less than 10 GiB free on the
  disk; yes on an NVIDIA GPU on the nvidia driver or an AMD one on amdgpu with 6 GiB of its own, or on Apple Silicon
  with 16 GiB; else yes on the CPU, slowly, with 16 GiB of memory; else no. Intel GPUs are not counted. The reason
  names each GPU set aside and why. It reads what the machine has, not its live load.
- **The rule** (`machine.DefaultPolicy`, every threshold a field): one worker per 2 cores, and per 2 GiB of memory
  beyond 2 GiB kept for the system, the smaller, between 1 and 16. A worker is mostly an agent waiting for its
  model; heavy commands go through gates. `djinn up --workers N` (or `$DJINN_WORKERS`) sets it by hand, 1 to 16.
- **Pressure** stops new workers and gates; what runs goes on. With PSI: tasks waited for the CPU 50% of the last
  10 s, or for memory 10% ("some"). Without: a load of 2 per core, or less than 10% of memory available; on macOS,
  the system's warning level.
- **The cost of a command** (`CommandService`, `djinn command list [--project <name>]`): `djinn gate run` measures
  the command it runs and sends it once the command ended by itself; one interrupted, or killed by a signal, is not
  recorded. A `CommandCost` per project and command (case ignored, up to 200 characters, as run): the runs, the mean
  CPU time and duration, the highest peak memory, and the last run. The project is the task's (`$DJINN_TASK_ID`),
  else the deepest project whose folder holds the working directory; outside any project, nothing.
- **How it is measured.** Linux and macOS: the `wait4` resource usage of the command. The CPU time counts the
  command and every child it waited for; the peak memory is the resident memory of its largest process, not the sum
  of a tree, and it has a floor: the djinn client's own memory before the command replaced it (about 20 MB). A child
  left running when the command ends is not counted. macOS gives the same numbers (in bytes, not KiB); not run on a
  Mac yet. Windows: the CPU time and the duration; no peak memory, which needs a Job Object around the command (0).
  A cgroup per command would count a whole tree, but a systemd scope disappears with its last process, before its
  `memory.peak` can be read.
- **What each worker uses** (`Task.resources`, `Machine.worker_uses`): every 5 seconds while a worker runs, Djinn
  reads its process group and every process it started, even one in a group of its own (an agent's tool may start
  one): the processes, the CPU in percent of one core since the reading before, and the resident memory summed over
  them, a shared page counted once per process. Linux: `/proc/<pid>/stat`; macOS: one `ps` for all the workers, at
  most once a second; Windows: not measured yet (`worker_measure` says so, and the Tasks tab). The task keeps the
  latest reading and the peaks over all its workers; a new worker clears the latest. It is written (`harness/measure`
  in the journal) only when it moved enough to show: a process more or less, 5 points of CPU, 5% of memory, or a
  minute gone; `djinn machine show` gives every reading. The fake agent runs in Djinn's process: not measured. The
  scheduler reads the peaks (below).
- **A gate waits for the memory** (`Policy.Room`, `CommandMargin` a field of `machine.DefaultPolicy`). This default
  rule is Djinn's recommendation, and the developer may change it: a gate goes to a command measured in its project
  only when there is no pressure and the memory available, less the peaks of the commands holding a gate, is at least
  its highest peak plus 512 MiB. The peaks of the holders count in full, even when they have not reached them yet or
  are already in what is available: careful rather than exact. A command never measured (or measured at 0, on
  Windows), outside any project, or on a machine whose memory is unknown goes as before. The command is the
  `what` of `GateService.Hold` (what `djinn gate run` runs, up to 200 characters), case ignored; its project is the
  task's, else the one holding its `directory`, as for the costs. Its peak is read once, when it asks for the gate.
  While it waits, the reason names the command, its peak and the memory free (`waiting: go tool task e2e peaks at
  3.1 GiB, 1.8 GiB free`), told again only when the cause changes, not as the memory moves. A heavy command that
  waits does not keep its gate from a lighter one behind it. The scheduler does not read the costs yet.
- **A worker waits for the memory** (`Policy.WorkerRoom`, `WorkerPeak`, `WorkerPeaks` and `WorkerMargin` fields of
  `machine.DefaultPolicy`). Once a slot is free and there is no pressure, a worker starts only when the memory
  available, less what the workers running may still take, is at least the typical peak of a worker of its provider
  plus 512 MiB. The typical peak is the median of the peak memory (`Task.resources`) of the latest 10 finished
  workers of that provider (a task with an end time and a peak, the latest ended first; with an even count, the mean
  of the two in the middle); 1 GiB while none is measured (the fake agent, a new provider, Windows). A running worker
  may still take that typical peak less what it uses now, one not read yet all of it, a worker the same pass starts
  all of it; a paused one nothing. The reason names the peak, how it was found, the memory free and what the
  running workers may take (`waiting: a claude worker peaks at 1.5 GiB (the median of the last 3 measured), 2.2 GiB
  free, 512 MiB of it for the workers running, 512 MiB kept`). The first task the memory holds holds the ones after
  it (`W5 goes first: …`): a resumed task keeps its turn, and a lighter worker never takes the turn of a heavier one.
  It holds with `djinn up --workers` too, as the pressure does. A machine whose memory is unknown holds no worker
  back. The memory is read at most once a second, with the rest.
- **A scope per worker** (`machine.Scopes`, `harness.WithScopes`): at start, on Linux, `djinn up` starts one probe
  scope as a worker's would be (`systemd-run --user --scope --quiet --collect --unit=djinn-probe-<uuid8> [-p
  CPUQuota=…%] [-p MemoryMax=…] -- sh -c …`), and reads from inside its cgroup, its `cpu.max` and `memory.max`. Then
  every process a worker starts (an agent, each run of a watcher's command, a warm worker, an inbox source's command)
  runs under the same prefix, in a scope of its own, `djinn-<task code>-<uuid8>.scope` (`warm`, `inbox` for those
  without a task); the uuid8 is drawn at each start: systemd keeps the name of an ended scope a little while, and the
  same name again fails one time in two. The scope keeps the process: same PID, process group and streams. Its cgroup
  is the probe's slice plus the unit, known before it exists. With it: pausing freezes the cgroup (`cgroup.freeze`)
  and resuming thaws it; stopping sends SIGTERM to the group and to each process of the cgroup outside it, then thaws,
  and after the grace delay kills the group and the cgroup (`cgroup.kill`, since Linux 5.14); measuring reads
  `cgroup.procs`, `cpu.stat` (`usage_usec`, ended processes included), `memory.current` (what the kernel charges: the
  page cache the worker filled too) and `memory.peak` (since 5.19), which the task's peak takes. Until systemd made
  the cgroup, or without the memory controller, all of it goes by the process group and /proc, as before. Without
  systemd-run, without a user systemd (no bus, as in CI), or off Linux, `djinn up` prints why once and workers run in
  their process group. `--worker-cpu` (`CPUQuota`) and `--worker-memory` in MiB (`Policy.WorkerMemory`, `MemoryMax`,
  0 by default) are properties of that scope; a cap whose controller systemd does not delegate to the user (Ubuntu
  22.04 delegates memory and pids, not cpu) is dropped, saying why, and the scope stays. Past the memory ceiling the
  kernel reclaims, then kills a process of the worker; Djinn does not say so yet.

## From T22 · Workers that start fast, with the right context, and are measured

**Goal.** A separate worker process costs what an in-process sub-agent does not: it rebuilds its
context and starts cold. We refuse that loss: every worker is measured, starts fast, and gets
the context its task needs, no more.

### Decided
- **Four levers, chosen task by task:**
  1. **Fork from the lead** when the worker needs the lead's whole context: a new process that
     starts from the lead's conversation with a new session (`claude -p --resume <lead> --fork-session`),
     measured and stoppable on its own.
  2. **Otherwise a brief built from the store**, behind a stable prefix (Djinn's rules, then the
     project's) placed before the prompt's dynamic boundary, so every worker of a project reads
     it from the cache.
  3. **Warm workers**: one process per active project, already loaded, waiting for its brief on
     its stream-json input. Their number is bounded by what the machine can take (T17).
  4. **Measure everything**: each process reports its usage in its stream; Djinn sums it by
     worker and by wish (done for Claude in T07).
- **Inheriting is not free**: a forked context is read again at every turn of the worker, even
  from the cache. A short brief is often cheaper and keeps the worker on its task.
- **Measure before promising**: a bench compares five ways to run the same small task (an
  in-process sub-agent, a cold `claude -p`, `--bare`, a fork of the lead, a warm worker): time
  to first token, tokens with cache, cost. It is a paid run: its cost is estimated and approved
  before it runs.

### Done when
- [x] The bench has run, and its figures are in this file (Haiku, below). Opus waits for a go.
- [x] A task can ask for a fork or a brief, and Djinn picks the brief by default.
- [ ] Warm workers are on, bounded by the machine, and their idle cost is known. *Built, bounded and off by
  default (`djinn up --warm-workers`); their memory is supposed until the bench measures it.* (needs: the paid
  bench, `go tool task bench-workers`, with a person's go)
- [x] Every worker's tokens and cost show per wish in the interface. (branch `w30-ui`: each task shows its tokens and
  its cost, the detail on hover and opened (input, output, cache read, cache written); a wish shows its total, and the
  flight plan each active wish's; Codex and Antigravity give tokens only, so their tasks show no cost and the total
  says how many have none. `src/usage.tsx`, `tests/data-flight.test.mjs`, the token tests of
  `tests/screens.test.mjs`, `e2e/flight-plan.spec.ts`)
- [x] A lead starts from a brief built out of the store (`djinn wish brief`, T13).

### Decided along the way
- **The brief is the lamp's, not a model's.** `djinn wish brief <wish>` (`WishService.Brief`) writes it in Go from
  the store: first the stable part, Djinn's rules for a lead and where each project keeps its own (`AGENTS.md`,
  `CLAUDE.md`, `CONTRIBUTING.md`, found at its root); then the part that moves: the title, the state, the open
  questions, the latest decisions, what runs, what waits, what finished, the latest blocks. The stable part names no
  wish: every lead of the same projects reads the same bytes, from the cache. Text goes through the export's
  scrubber (no project folder, no home folder, no data folder) and URLs lose their credentials. Empty sections are
  hidden. `plan.StableBrief` gives the stable part alone.
- **A new lead starts from it** (T13). *Since T21, every new lead, of any agent, gets one first message, the same for
  all: run `djinn wish brief <wish>`, then continue (`plan.StartLine`, written to `lead-first.md`); the brief opens
  with the wish and ends with the rules, and the system prompt file below is gone. What follows is the first way.*
  `djinn wish resume <wish>` without a lead session wrote the two parts next
  to the wish's page (`lead-rules.md`, `lead-brief.md` in the data folder) and runs, through the user's shell,
  `claude --session-id <new> --append-system-prompt-file <lead-rules.md> '<the moving part>'`. The session is
  recorded as the wish's lead once the terminal runs, so the next resume resumes it. `--append-system-prompt-file`
  is hidden from `claude --help` (2.1.293): verified in the binary ("Read system prompt from a file and append to
  the default system prompt") and in the help of `--bare`. cmd.exe cannot carry a line break, nor a brief longer
  than 64 KiB any shell: the first message then asks to read `lead-brief.md`, with `--add-dir` on its folder.
  **Codex** (`codex '<brief>'`) and **Antigravity** (`agy -i '<brief>'`, verified in `agy --help` 1.3.0) take no
  system prompt at their command line: the whole brief is their first message, stable part first. Djinn cannot
  choose their session ahead: a codex lead is recorded with `djinn wish set-lead`, an agy one is never resumed.
  *Supposed:* that interactive `codex` takes a prompt as its argument (codex is not installed here).
- **Fork, on request only**: `djinn task spawn <wish> --fork W1` or `--from-lead` (`Task.fork_session`,
  `Task.fork_of`). Claude: `claude -p --resume <source> --fork-session --session-id <task>` (claude refuses
  `--session-id` with `--resume` unless it forks: seen in the 2.1.293 binary), so the fork's session is the task's
  identifier, as for any task. Codex: `thread/fork` with the source thread, already in the schema. Antigravity
  cannot fork: refused before the task exists, as are a source without a session yet and an agent other than the
  source's (the fork runs the source's agent by default). A planned fork keeps its source until it starts; an export
  leaves `fork_session` out. *Supposed:* that claude finds the source session from the fork's own worktree, another
  folder than the source's (claude files sessions by folder): the bench's fork runs check it.
- **Warm workers, the minimum safe**: `djinn up --warm-workers`, off by default. One claude per project of each
  active wish, started ahead with `--input-format stream-json` and no message: it loads, then waits. The next task
  spawned there takes it: its identifier was chosen ahead, so the session (`--session-id`), `DJINN_TASK_ID` and the
  worktree are already the task's; the placeholder branch (`djinn-warm-…`) is renamed after the task. The scheduler
  starts the next one. They live in the slots the running workers leave (running + warm ≤ the machine's slots), the
  first wish first, and none waits while the machine is under pressure. A task that asks for anything the warm worker
  was not started with starts cold: another model or budget than the project's settings give
  (`TestWarmTakesTheProjectSettings`), a fork, other rights (`.agents/permissions.txtpb` or the wish's allowance
  changed), a project whose HEAD moved, a planned task (its identifier is older). Stopping Djinn stops them and
  removes their worktrees; a crash's leftovers (a worktree in the data folder on a `djinn-warm-` branch) are
  removed at the next start. Only Claude warms (`harness.Warmer`).
- **What a warm worker costs while it waits: no token, memory.** *Supposed: about 300 MB each* (claude 2.1.293 is a
  250 MB single-file Bun program; a loaded Node or Bun agent sits at a few hundred MB). The fake binary's figure would
  mean nothing; the bench reads the real one (`ps -o rss`) before the warm worker's message, which costs nothing.
- **The bench compares what Djinn launches**: cold, `--bare`, a fork, a warm worker, and a brief (a cold worker with
  the stable brief in `--append-system-prompt-file` and `--exclude-dynamic-system-prompt-sections`, which moves
  the folder and Git status out of the system prompt, for the cache). The in-process sub-agent is left out: Djinn
  does not launch it.

- **What the window shows of a worker's spending** is `Task.usage`, as the harness sums it run after run: never a
  figure of its own. A cost shows only above zero: Codex and Antigravity give none, and the page does not price their
  tokens. A wish's total sums the tokens of all its tasks and the costs it has, and counts the tasks without one.

### The bench
`go tool task bench-workers` (`tools/benchworkers`). Without the developer's go it prints its quote and refuses:

```sh
go tool task bench-workers                                          # the quote, nothing runs
BENCH_PAID=yes BENCH_MAX_USD=0.50 go tool task bench-workers         # Haiku, 3 rounds: about $0.10 ($0.05 to $0.21)
BENCH_PAID=yes BENCH_MAX_USD=7 BENCH_MODEL=opus go tool task bench-workers   # about $3.15 ($1.57 to $6.30)
```

- **What it does.** A Git repository in a temporary folder (`README.md` says "The answer is 42", `AGENTS.md` two
  rules). One parent session reads them, in the repository itself, for the forks. Then each round runs the five
  variants in turn, each in a worktree of its own, as Djinn gives each task: "Read README.md and reply with the
  answer it gives". `BENCH_RUNS` rounds (3), `BENCH_MODEL` (haiku), `BENCH_WARM_WAIT` (5s of loading before the warm
  worker's message), `BENCH_OUT` (a file for the table).
- **What it measures**, per run then as medians: the time from asking (the start of the process; the message, for a
  warm worker) to the first output and to the result; claude's own `ttft_ms`; the tokens read from the cache, written
  to it, the other input and the output; the cost claude reports; a warm worker's memory while it waited.
- **The quote** comes from real runs recorded in `internal/harness/testdata/claude`: $0.006 a small Haiku 5.5 run,
  $0.18 a first Opus 5.5 call (22k tokens written to the cache). 5.5 runs a round (a fork reads its parent again)
  plus the parent. A model not observed is quoted as Opus. The cap must cover the quote; the bench stops once it has
  spent it, and gives each worker what is left as `--max-budget-usd` (claude checks it after a call).
- **To know before running.** `--bare` reads no OAuth sign-in: with a subscription and no `ANTHROPIC_API_KEY`, the
  bare runs fail at once, for free, and say so. The sessions land in claude's own folder, under the temporary
  folder's name. The cache makes the rounds after the first cheaper: compare medians, not the first round.

#### First run, Haiku, 3 rounds ($0.06)

2026-10-08, model haiku, 3 rounds, $0.0616 spent. Medians:

| Variant | Runs ok | First output | Result | Claude's ttft | Cache read | Cache write | Input | Output | Cost | Warm RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| cold | 3 of 3 | 2068 ms | 3524 ms | 1507 ms | 38153 | 12845 | 4 | 409 | $0.0032 | - |
| bare | 0 of 3 | 0 ms | 0 ms | 0 ms | 0 | 0 | 0 | 0 | $0.0000 | - |
| fork | 3 of 3 | 2034 ms | 2954 ms | 1337 ms | 78665 | 32625 | 8 | 484 | $0.0076 | - |
| warm | 3 of 3 | 2136 ms | 2697 ms | 1728 ms | 43648 | 14546 | 4 | 300 | $0.0035 | 238 MB |
| brief | 3 of 3 | 2284 ms | 3554 ms | 1850 ms | 38610 | 15398 | 4 | 338 | $0.0036 | - |

What it says, on a tiny task with a small parent:
- **A cold start is cheap**: about 2 s to the first output. The CLI's own start is not the cost to fight.
- **Warm saves about 0.8 s** to the result (2.7 s against 3.5 s), for 238 MB of memory while it waits.
- **A fork reads twice the cache and costs twice as much**, from a parent of only 40k tokens. From a lead of 150k
  tokens, the gap grows with every turn: a brief stays the default.
- **A brief costs what a cold start costs**, and carries the wish's state.
- **`--bare` needs an API key**: it does not read the OAuth login. Not a path for people on a subscription.

### Open questions
- Codex and Antigravity: what forking and resuming each offers (T07, T18). *Codex forks (`thread/fork`), agy does
  not; neither takes a system prompt at the command line; codex's lead session is not known ahead.*
- Workers behind the stable brief: once the bench shows its gain, give every claude worker the project's stable part
  in `--append-system-prompt-file`? *Recommendation: decide on the bench's figures.*
- Telemetry of a lead's own in-process sub-agents needs an endpoint: open a local port for it
  only, or do without?

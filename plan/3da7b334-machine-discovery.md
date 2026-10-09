---
id: 01a11888-644b-7e58-9f2c-390e3da7b334
code: T17
phase: 2
status: in-progress
---

# T17 · Know the machine, spend it wisely

**Goal.** The machine is a resource like any other. Djinn knows what it has, learns what each
tool costs, and decides how many workers to start and what they may run, so the machine is never
overloaded. Part of the orchestrator (T07).

## What to do
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

## Done when
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

## Decided along the way
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

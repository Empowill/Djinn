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
  minute gone; `djinn machine show` gives every reading. The fake agent runs in Djinn's process: not measured. Not
  read by the scheduler yet.
- **Not used yet.** The scheduler and the gates do not read the costs yet: a heavy command waiting for a gate
  (the capacity, above) is the next step.

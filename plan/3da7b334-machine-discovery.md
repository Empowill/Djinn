---
id: 01a11888-644b-7e58-9f2c-390e3da7b334
code: T17
phase: 2
status: open
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
- [ ] `djinn machine` shows the discovered machine and the live load.
- [ ] After a few runs, each project command has a measured cost.
- [ ] Djinn never starts more workers than the machine holds, and says why it waits.

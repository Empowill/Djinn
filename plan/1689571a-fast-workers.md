---
id: 01a118c9-246f-7a3d-8702-04081689571a
code: T22
phase: 2
status: open
---

# T22 · Workers that start fast, with the right context, and are measured

**Goal.** A separate worker process costs what an in-process sub-agent does not: it rebuilds its
context and starts cold. We refuse that loss: every worker is measured, starts fast, and gets
the context its task needs, no more.

## Decided
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

## Done when
- [ ] The bench has run, and its figures are in this file.
- [ ] A task can ask for a fork or a brief, and Djinn picks the brief by default.
- [ ] Warm workers are on, bounded by the machine, and their idle cost is known.
- [ ] Every worker's tokens and cost show per wish in the interface.

## Open questions
- Codex and Antigravity: what forking and resuming each offers (T07, T18).
- Telemetry of a lead's own in-process sub-agents needs an endpoint: open a local port for it
  only, or do without?

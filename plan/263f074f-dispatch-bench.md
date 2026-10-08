---
id: 01a11884-1974-7b62-a764-2e27263f074f
code: T16
phase: 3
status: in-progress
---

# T16 · Dispatch: plain Go code or a local model?

**Goal.** Decide, on numbers, whether dispatching needs a model at all. Dispatch only: which ready
task starts now, on which worker, once its dependencies, write scopes and gates allow it. Deciding
*what* the tasks are, or recommending sub-agents, stays with the large model that plans the wish.

## Depends on
- **T17, knowing the machine.** Whether a local model can run at all comes from the discovery
  (a usable GPU and its drivers, the memory), and a dispatcher must respect the machine's budget
  whatever it is made of.

## The contenders
- **Plain Go code**: the scheduler of the orchestrator (T07). Deterministic, free, instant.
  Preferred: if it covers the cases, no model is needed.
- **A local open-weight model**: Gemma 4 (Apache-2.0, native tool calling), run by Ollama on the
  machine, given the same inputs and asked for the same decision.

- **Consensus across machines** (Raft and the like) is a different question: see T15.

## The benchmark
- A fixed set of dispatch situations, as data: task graphs, overlapping write scopes, busy gates,
  machine pressure, a worker that failed, a question that blocks.
- For each, the expected decision, written by hand.
- Measured for each contender: decisions right, decisions wrong, time per decision, CPU and memory
  used on a laptop without a GPU and on an Apple Silicon Mac.
- The model gets a case only where the Go code has no rule; if there is none, the model has no role.

### The Go side (done)
- The scheduler's rules are a pure function, `internal/dispatch`: a `Situation` (tasks, wishes, projects in Git,
  the machine's slots, running workers and pressure) and a `Decision` per planned task (start, wait with its reason,
  fail). The harness acts on it; nothing else changed in what it decides.
- The cases: `internal/dispatch/bench/cases.json`, 31 situations written by hand with the expected decision of each
  planned task: dependencies (running, done, chains, failed, stopped, interrupted, gone, waiting for a question),
  write scopes (outside Git, in Git, the whole folder, case, another project, two planned writers), the machine
  (pressure, full, one slot for two), wish ranks (rank, then age, unranked, paused, granted), a busy gate. JSON, so
  that a model reads the same file.
- `go tool task bench-dispatch` prints the table. `TestGo` checks that Go decides every case as expected.
- First run (linux/amd64, 16 cores): 31 of 31 cases right, 42 of 42 decisions, about 7 µs per pass, the
  situation built included.
- What the cases show: Go has a rule for each of them. Gates are not a dispatch decision: a worker waits for its gate
  when it runs the command, so a busy gate holds no task. A question blocks only through its task's state (waiting).

## Done when
- [ ] The benchmark runs with one task command and prints a table. (Go side done: `go tool task bench-dispatch`,
  `TestGo` in `internal/dispatch/bench`; needs: Ollama with Gemma 4 on the machine, and a decision to download it,
  for the model side)
- [ ] A decision is written down: Go only, or Go plus a model for named cases, with the numbers. (needs: the
  bench, then a person to decide)

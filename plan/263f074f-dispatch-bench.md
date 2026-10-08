---
id: 01a11884-1974-7b62-a764-2e27263f074f
code: T16
phase: 3
status: open
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

## The benchmark (planned, not run yet)
- A fixed set of dispatch situations, as data: task graphs, overlapping write scopes, busy gates,
  machine pressure, a worker that failed, a question that blocks.
- For each, the expected decision, written by hand.
- Measured for each contender: decisions right, decisions wrong, time per decision, CPU and memory
  used on a laptop without a GPU and on an Apple Silicon Mac.
- The model gets a case only where the Go code has no rule; if there is none, the model has no role.

## Done when
- [ ] The benchmark runs with one task command and prints a table.
- [ ] A decision is written down: Go only, or Go plus a model for named cases, with the numbers.

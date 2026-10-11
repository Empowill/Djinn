---
id: 01a126be-97d5-74de-aeb1-967183566b82
code: T30
phase: 2
status: draft
---

# T30 · Dispatch: local model research

**Goal.** Decide, on numbers, whether dispatching needs a local open-weight model through benchmarked dispatch situations.

## The research question
Decide, on numbers, whether dispatching needs a model at all. Dispatch only: which ready
task starts now, on which worker, once its dependencies, write scopes and gates allow it. Deciding
*what* the tasks are, or recommending sub-agents, stays with the large model that plans the wish.

### Depends on
- **T17, knowing the machine.** Whether a local model can run at all comes from the discovery
  (a usable GPU and its drivers, the memory), and a dispatcher must respect the machine's budget
  whatever it is made of. (The verdict exists: `can_run_local_model` and its reason, in `djinn machine show`)

### The contenders
- **Plain Go code**: the scheduler of the orchestrator (T07). Deterministic, free, instant.
  The Go rules are already benchmarked in `internal/dispatch/bench` (`go tool task bench-dispatch`).
- **A local open-weight model**: Gemma 4 (Apache-2.0, native tool calling), run by Ollama on the
  machine, given the same inputs and asked for the same decision.

### The benchmark
- A fixed set of dispatch situations, as data: task graphs, overlapping write scopes, busy gates,
  machine pressure, a worker that failed, a question that blocks (`internal/dispatch/bench/cases.json`).
- For each, the expected decision, written by hand.
- Measured for each contender: decisions right, decisions wrong, time per decision, CPU and memory
  used on a laptop without a GPU and on an Apple Silicon Mac.
- The model gets a case only where the Go code has no rule; if there is none, the model has no role.

## Done when
- [ ] The benchmark runs with one task command and prints a table for the local model. (needs: Ollama with Gemma 4 on the machine, and a decision to download it, for the model side)
- [ ] A decision is written down: Go only, or Go plus a model for named cases, with the numbers. (needs: the bench, then a person to decide)

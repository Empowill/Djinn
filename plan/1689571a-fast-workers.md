---
id: 01a118c9-246f-7a3d-8702-04081689571a
code: T22
phase: 2
status: in-progress
after: T07 T13 T17
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

## Decided along the way
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

## The bench
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

### First run, Haiku, 3 rounds ($0.06)

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

## Open questions
- Codex and Antigravity: what forking and resuming each offers (T07, T18). *Codex forks (`thread/fork`), agy does
  not; neither takes a system prompt at the command line; codex's lead session is not known ahead.*
- Workers behind the stable brief: once the bench shows its gain, give every claude worker the project's stable part
  in `--append-system-prompt-file`? *Recommendation: decide on the bench's figures.*
- Telemetry of a lead's own in-process sub-agents needs an endpoint: open a local port for it
  only, or do without?

---
id: 01a123d0-5461-74ee-889e-83c39ac1c08b
code: T27
phase: 2
status: in-progress
---

# T27 · Djinn stays fast, whatever the size of a wish: the window, djinn up and its start

**Goal.** Djinn stays fast and responsive whatever the size of a wish, across the window, djinn up, and its startup.

The developer's words: "The window renders everything on every change: opening a wish and switching tabs are slow on a real wish (~170 tasks, 131 decisions, 250 blocks)." Decisions Q61 and Q62 set the path: measure first with a reproducible real-size wish (W174), fold finished work and memoize cards first (W177, W178, W248), and virtualize only if measured timings miss the budget.

**What was decided.**
- **Real-size wish fixture** (W174, `internal/testx/bigwish`): 180 work tasks, 28 azimas, 62 questions (61 decisions), 253 blocks, 964 journal commands, and ~26 700 events. Deterministic generation with fixed seed and clock (`TestSizes`, `TestDeterminism`).
- **Store and stream benchmarks** (W227, `internal/server/wish_bench_test.go`): measure store read, JSON and protobuf serialization, and change streaming over Unix socket under `go tool task bench` (`BenchmarkWishStoreRead`, `BenchmarkWishServerSend`, `BenchmarkWishChange`).
- **E2e render spec and millisecond budgets** (W247, W253, `e2e/wish-render.spec.ts`): measures opening a wish, tab switches (tasks, decisions, journal, blocks), opening a task, and live change arrival in the browser build under Playwright.
- **Folding and memoization** (W177, W178, W248):
  - Done tasks and azimas are folded behind fold lines (`src/task-tabs.tsx`, `Fold`).
  - Older decisions and journal entries unfold in batches of 30 (`src/older.tsx`, `useRecent`). What is folded is not rendered into the DOM.
  - Task, decision, journal, and question cards are memoized with `React.memo` and stable callbacks so they re-render only when their data changes.
- **Virtualization verdict (Q62, answer D)**: after folding and memoization, all measured timings comfortably beat their budgets on the real-size wish. Virtualization is not needed.

## Timings (Linux, 3 runs median on real-size wish)

| Action | Measured | Budget | Result |
| --- | --- | --- | --- |
| Open wish | 29.4 ms | 300 ms | Pass |
| Tab tasks | 30.2 ms | 250 ms | Pass |
| Tab decisions | 53.2 ms | 250 ms | Pass |
| Tab journal | 22.8 ms | 200 ms | Pass |
| Tab blocks | 20.9 ms | 250 ms | Pass |
| Open task | 20.9 ms | 200 ms | Pass |
| Change arrival | 75.4 ms | 500 ms | Pass |

Raw runs:
- Open wish: [36.8, 27.1, 29.4] ms
- Tab tasks: [30.2, 26.7, 39.8] ms
- Tab decisions: [64.8, 52.4, 53.2] ms
- Tab journal: [22.8, 29.1, 22.3] ms
- Tab blocks: [20.9, 18.1, 22.0] ms
- Open task: [28.6, 20.9, 17.6] ms
- Change arrival: [72.7, 75.4, 86.5] ms

## Done when

- [x] A reproducible real-size wish fixture generates the same bytes on every run for benchmarks and e2e specs. (`internal/testx/bigwish`; `TestSizes`, `TestDeterminism`)
- [x] Server benchmarks measure store read, serialization and streaming on real and x10 wish sizes. (`internal/server/wish_bench_test.go`; `BenchmarkWishStoreRead`, `BenchmarkWishServerSend`, `BenchmarkWishChange`)
- [x] Long lists fold finished items and unfold older entries in batches so hidden items are not rendered. (`src/task-tabs.tsx`, `src/older.tsx`)
- [x] Cards are memoized so only changed items re-render. (`src/wish-task.tsx`, `src/azima.tsx`, `src/decision-log.tsx`, `src/journal.tsx`)
- [x] An e2e spec measures opening a wish, tab switches, opening a task, and change arrival against millisecond budgets, and all hold on the real-size wish. (`e2e/wish-render.spec.ts`: open wish ~29.4 ms <= 300 ms, tab tasks ~30.2 ms <= 250 ms, tab decisions ~53.2 ms <= 250 ms, tab journal ~22.8 ms <= 200 ms, tab blocks ~20.9 ms <= 250 ms, open task ~20.9 ms <= 200 ms, change arrival ~75.4 ms <= 500 ms)
- [ ] Measure startup time and memory of `djinn up` on a real-size wish. (needs: startup profiling)

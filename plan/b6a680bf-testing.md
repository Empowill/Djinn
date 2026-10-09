---
id: 01a1184f-cf19-7585-8946-4b29b6a680bf
code: T05
phase: 1
status: done
---

# T05 · Testing

**Goal.** Iterate fast, and let the coding agent check its own work end to end.

## Decided
- **Unit tests in seconds** (`go tool task test`): the ORM on in-memory SQLite, the CLI
  convention as reference tests, Connect handlers called in memory. The existing Node tests
  and their fake Claude and Codex providers stay, and specify the Go port.
- **End-to-end in a browser** (`go tool task e2e`): `djinn up --browser`, driven by
  Playwright headless, with the fake providers and a temporary config folder. No model is
  called, nothing touches real projects.
- **The browser is the end-to-end reference to start with.** The native window gets a smoke
  test, and a manual check on macOS. Wails v3 ships no WebDriver on Linux or macOS.
- **To improve: test the native window end to end.** Candidates: the experimental MCP server
  Wails runs in dev mode (DOM queries, clicks, typing in the real window), or a WebDriver
  bridge on Linux.

- **Tests stay fast.** A Go test takes under a second: fake clocks (`WithClock`), short ticks (`WithTick`), fake
  workers that wait for a message (the fake's `wait` step) instead of sleeping, waits on events, `t.Parallel` where
  tests share nothing. `test-go` pipes `go test -json` to `tools/slowtests`, which fails on any test over 2 s; its
  allowlist stays empty, or says why.

## Timings
Linux, 16 cores, `go test -tags headless -count=1`, under a gate.

| | Before (09/10) | After (09/10) |
|---|---|---|
| `internal/harness` alone | 44.4 s (103 tests, one after the other) | 3.7 s |
| All Go packages, wall time | 56.7 s | 11.3 s |
| Slowest Go test, all packages at once | 7.44 s (`TestUpdateFromRelease`) | 1.76 s |
| Slowest Go test, one package at a time (`-p 1`) | 7.44 s | 1.46 s (`TestUpdateFromRelease`) |
| Go tests over 1 s, one package at a time | 19 | 4 (`TestUpdateFromRelease`, `TestRestartResumesInOrder`, `TestWatcherResumes`, `TestWatcher`: 1.1 to 1.5 s) |
| `go tool task test` (Go, interface, 31 e2e specs) | about 3 min (not measured here) | 1 min 55 s (e2e 1.3 min) |

Run together, the packages share the cores: a test then takes up to twice its time alone, under the 2 s of the guard.
What remains is mostly the store's durable writes (`synchronous(FULL)`, kept: nothing is lost) and real processes.

## Done when
- [x] `task test` runs in seconds and needs nothing running. (08/10, Linux: `test-go` 23 s uncached, `test-ui`
  1.4 s, `e2e` 18 s, all pass; e2e starts its own djinn in a temporary folder)
- [x] `task e2e` runs the existing Playwright specs against the real Go server. (08/10: 13 specs pass against
  `bin/djinn-e2e up --browser`)
- [x] The window smoke test runs on Linux. (`go tool task check-window` on WebKitGTK, 08/10, recorded in T01; not
  run again here: it opens a window)
- [x] An agent drives the native window end to end, not only the browser. (`go tool task e2e-native` on Linux,
  recorded in T06; not run again here: it opens a window)

---
id: 01a11855-a8d2-782a-a628-9a70a586b68b
code: T11
phase: 1
status: in-progress
after: T01 T06
---

# T11 · Windows

**Goal.** Djinn runs on Windows as well as on macOS and Linux. Every architecture choice is
checked against Windows first, because it is where the constraints are.

## Decided
- **On Windows, the window and the command line use the loopback HTTP server** (127.0.0.1, free
  port, token, Origin check), with the Connect API on the same origin. On macOS and Linux they
  use the Wails internal asset server and a Unix socket, with no port. WebView2 cannot stream a
  response served through its custom request handler: the content must be complete when the
  request event ends
  ([Microsoft docs](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2webresourceresponse)).
  A real HTTP server streams. `go tool task check-window` proves the path of each system.
- **No Wails JavaScript bindings.** Native features (pick a folder, notify) are Connect methods
  implemented in Go with the Wails Go API.
- **The command line finds the server through `server.addr`** in the data folder: a Unix socket on
  macOS and Linux, loopback HTTP with a token on Windows.
- **Paths from the standard library** (`os.UserConfigDir`, `filepath`), never hard-coded.
- **Task commands stay portable**: Task runs them with its own shell interpreter; no Unix-only
  tool in `Taskfile.yml`.
- **The terminal's pseudo-console is ConPTY written on `golang.org/x/sys/windows`**
  (`internal/terminal/pty_windows.go`), maintained by the Go team, in place of Charm's `x/conpty` (experimental, no
  promises). Two pipes, `CreatePseudoConsole`, the program started suspended with a `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`
  attribute list, put in a Job Object, then resumed: a hangup closes the console (CTRL_CLOSE_EVENT), a kill ends the
  job, the program and what it started. The job kills its processes once its last handle closes: djinn ending without
  closing its terminals (a crash, the e2e's kill) ends their programs, as the kernel hangs up a pseudo-terminal on
  Unix, and nothing keeps their folder (W131: the e2e's teardown failed on `EBUSY … shell`). A hung-up terminal ends
  what is left of its job when it closes, a child that outlived its program on CTRL_CLOSE_EVENT included; a program
  that ended by itself leaves a window it started apart running (the limit is lifted before the job closes). Once no
  process of the job is left, the console closes at once and its output ends, without the second left to a child
  holding the terminal.

## To check on Windows
- The Wails v3 window on WebView2, built with or without CGO (to confirm).
- The agent CLIs: Claude Code and Codex on native Windows, or through WSL (to confirm).
- Worktrees and long paths: keep the worktree root short and set `core.longpaths`.
- The machine monitor: no cgroups and no pressure stall information. Job Objects give per-worker
  CPU and memory accounting, limits and kill-the-whole-tree; pausing a worker has no simple
  equivalent to `SIGSTOP`.
- Atomic writes: renaming over a file another process holds open fails on Windows ("Access is denied", seen by W72
  in the CI log of `TestSync`). A browser reading `page.html`, or an antivirus scanning it, holds it a moment. The
  plan's `writeFile` (`internal/plan/exchange.go`: pages and exports) tries the rename again for 500 ms on Windows
  only (`TestRenameRetrying` with an injected rename; `TestWriteFileWhileOpen` holds the file open, which only
  Windows refuses). `ui.WriteAtomic` (state, settings, crash notes) and the backup's rename do not retry yet: files
  only Djinn reads.
- End-to-end on the native window: WebView2 accepts a remote debugging port, so Playwright can
  drive the real window there.

## Done when
- [ ] `go install`, then `djinn up`, opens the window on Windows 11 and a stream reaches it. (needs: a Windows 11
  machine; `GOOS=windows go vet -tags headless ./...` passes on Linux, 08/10)
- [ ] `task test` and `task e2e` pass on Windows. (needs: the CI's Windows job green. It runs test-go, test-ui and e2e
  since 09/10, still `continue-on-error`; the failures of its first run (09/10) are fixed but not yet seen green: W72
  checked each one again against the log of run 37913958476. W131 fixed the three of run 37997257519 (10/10), proof
  pending the next push: TestWatcher paused a watcher on Windows; TestRestartResumesInOrder lost F0, which the first
  harness's scheduler started after its 2 s tick, slower test set-up on Windows, and whose `git worktree add` djinn up
  stopping cut short, leaving it interrupted without a worktree, never resumed (now such a task waits again, as
  planned; the test writes the left-over tasks while djinn is down); the e2e's `EBUSY`, see the terminal above)
- [ ] A worker runs in a worktree on Windows, with its CPU and memory measured. (needs: a Windows machine, and the
  per-worker measure, not built: Job Objects, T17)

## How we test on Windows
- The unit tests run on Windows (`go tool task test`). Cross-checks from Linux:
  `GOOS=windows go vet ./...` type-checks the code and the tests for Windows.
- The CI's Windows job (`.github/workflows/ci.yml`) runs `test-go`, `test-ui` and `e2e` (browser mode, headless
  Chromium), each step reporting even when one before fails; it reports without blocking until it is green once.
  On a branch without a pull request, once the workflow's `workflow_dispatch` is on main:
  `gh workflow run ci.yml --ref <branch>`.
- The terminal's own tests on Windows (`internal/terminal/pty_windows_test.go`): read and write through `cmd.exe`, the
  exit code, the folder and environment, a resize seen by the program, a close that kills a program holding on
  through CTRL_CLOSE_EVENT with its child, a hangup that ends the child of a program that ended on it, and the
  programs of a terminal ending with a djinn killed without closing it. From Linux, `GOOS=windows go vet -tags headless ./...` and `GOOS=windows go
  test -c` only compile them: the proof is their run in the CI's Windows job (`test-go`), not yet seen green.
- What only Unix can run skips on Windows and says why: tests that stop djinn with a signal or run a shell script
  (`//go:build !windows` files, with their reason), the terminal specs that drive a POSIX shell, pausing a worker.
- Windows' temporary folder has a short 8.3 name (`RUNNER~1`); djinn stores a project's folder resolved, so a test
  comparing folders resolves its own (`filepath.EvalSymlinks`).
- `djinn gate run` counts a command's children through a Job Object on Windows (CPU time and peak memory).
- The native window is checked in one dedicated session on a maintainer's Windows machine, when
  enough Windows-relevant work has landed: `go install`, `djinn up`, `go tool task test`,
  `go tool task check-window`, `go tool task e2e`. Not after every change.
- Scripts, tasks and install steps are written for Windows too (see `AGENTS.md`). The scripts left
  from the Electron build (`scripts/package-local.cjs`, `scripts/mission-ui-check.cjs`) target
  macOS only: they go away with Electron (T09).

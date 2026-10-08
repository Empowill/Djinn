---
id: 01a11855-a8d2-782a-a628-9a70a586b68b
code: T11
phase: 1
status: open
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

## To check on Windows
- The Wails v3 window on WebView2, built with or without CGO (to confirm).
- The agent CLIs: Claude Code and Codex on native Windows, or through WSL (to confirm).
- Worktrees and long paths: keep the worktree root short and set `core.longpaths`.
- The machine monitor: no cgroups and no pressure stall information. Job Objects give per-worker
  CPU and memory accounting, limits and kill-the-whole-tree; pausing a worker has no simple
  equivalent to `SIGSTOP`.
- Atomic writes: renaming over a file another process holds open fails on Windows.
- End-to-end on the native window: WebView2 accepts a remote debugging port, so Playwright can
  drive the real window there.

## Done when
- [ ] `go install`, then `djinn up`, opens the window on Windows 11 and a stream reaches it. (needs: a Windows 11
  machine; `GOOS=windows go vet -tags headless ./...` passes on Linux, 08/10)
- [ ] `task test` and `task e2e` pass on Windows. (needs: a Windows machine, or the CI's Windows job green)
- [ ] A worker runs in a worktree on Windows, with its CPU and memory measured. (needs: a Windows machine, and the
  per-worker measure, not built: Job Objects, T17)

## How we test on Windows
- The unit tests run on Windows (`go tool task test`). Cross-checks from Linux:
  `GOOS=windows go vet ./...` type-checks the code and the tests for Windows.
- The native window is checked in one dedicated session on a maintainer's Windows machine, when
  enough Windows-relevant work has landed: `go install`, `djinn up`, `go tool task test`,
  `go tool task check-window`, `go tool task e2e`. Not after every change.
- Scripts, tasks and install steps are written for Windows too (see `AGENTS.md`). The scripts left
  from the Electron build (`scripts/package-local.cjs`, `scripts/mission-ui-check.cjs`) target
  macOS only: they go away with Electron (T09).

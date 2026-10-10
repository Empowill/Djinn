---
id: 01a1184f-cf19-7585-8946-4b29b6a680bf
code: T05
phase: 1
status: in-progress
after: T01
---

# T05 · Tested end to end

**Goal.** Tests run fast, and an agent checks its own work end to end, up to the real native window.

Iterate fast, and let the coding agent check its own work end to end.

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

## From T06 · End-to-end tests on the native window

**Goal.** An agent drives the real native window, not only the browser.

### How it works

- `go tool task e2e-native` builds a test variant, `bin/djinn-e2e-native`, with `-tags mcp`. The tag compiles in the
  MCP server of Wails; `App.Run` starts it on its own. The variant titles its window "Djinn e2e"
  (`cmd/djinn/window_mcp.go`). It is never shipped: a normal build has neither the server nor the title.
- The test, `e2e/native/`, runs that variant with a short temporary `DJINN_HOME`. Its MCP server listens on
  127.0.0.1 only, on a free port (`WAILS_MCP_PORT=0`) read from its log, behind a random token.
- Through the MCP server it checks these things:
  - The interface shows: the terminal and the wish view are in the DOM.
  - A wish made by the command line shows in the window without a reload, and a Mermaid block draws.
  - The terminal runs `whoami`, typed key by key, and shows the user name.
  - Closing the window minimises it (hides it on macOS). Djinn still answers a Connect call on its socket (Q36).
- It stops the variant by its PID and removes the folder. It opens a window, so `go tool task test` skips it.
  `DJINN_E2E_NATIVE_HOLD=5s` keeps the window up to look at it or capture it (`xwd -name "Djinn e2e"` on X11).

### Done when

- [x] One end-to-end scenario runs against the native window on Linux: `go tool task e2e-native`, about 3 s once
      built (Ubuntu 22.04, GTK 3, WebKitGTK 2.50).
- [ ] The same scenario runs on macOS. (needs: a Mac, `go tool task e2e-native` once)

### Decided along the way

- **The Wails MCP server, no WebDriver bridge.** It ships with Wails, works on every platform, and needs no code:
  a build tag. Its port and token come from the environment (`WAILS_MCP_*`).
- **The test build changes the pages, and only the pages** (`windowAssets`, the identity in a normal build):
  - Wails runs a window's scripts only once its runtime says "ready". The interface does not load the Wails
    runtime (no bindings), so each page loads a one-line `/djinn-e2e/ready.js` that sends that message.
  - Each MCP call posts its result back by `fetch` to `http://127.0.0.1:<port>`. The interface's
    `connect-src 'self'` forbids it, so the test build adds `http://127.0.0.1:*` to that one directive.
- **Typing goes through xterm, letters and digits only.** The MCP server gives a punctuation key the key code of
  another key (`'` is 39, the right arrow), and xterm drops a synthetic space. Hence `whoami`: no argument.
- **A JavaScript error comes back with its message.** WebKit gives the MCP server only the stack, so the test wraps
  each script in a `try`.
- **Go, not Playwright.** The MCP server speaks JSON over HTTP: no browser needed, and the test reads the socket
  address with the packages Djinn already has.

### What macOS and Windows need

- **macOS.** Run `go tool task e2e-native` once on a Mac. Expected to pass as is: WKWebView takes the same "ready"
  message, the page comes from the asset server too, and the close check expects a hidden window there. The
  temporary folder sits under `/tmp`, so the socket path stays short.
- **Windows.** Not yet. The window loads the loopback HTTP server, not the asset server, so `windowAssets` never
  sees the pages: the ready script and the CSP change must move to that server, behind the same tag. The terminal
  check skips (`whoami` prints the domain), and the test kills the variant (no interrupt on Windows).

### Open questions

None.

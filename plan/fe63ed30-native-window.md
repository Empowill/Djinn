---
id: 01a1184f-cf15-74c4-bd3f-4d72fe63ed30
code: T01
phase: 1
status: in-progress
---

# T01 · The native window

**Goal.** One Go command, `djinn`, opens a native window that embeds the existing React
interface, without Node or Electron at runtime.

## Decided
- [Wails v3](https://v3.wails.io): Go plus the system webview (WebKit on macOS and Linux,
  WebView2 on Windows). The Vite build (`dist/`) is embedded in the binary.
- The window loads the interface from Djinn's loopback HTTP server, with the Connect API on the
  same origin: protobuf, and real streaming on Windows, macOS and Linux (see T11). No Wails
  JavaScript bindings: native features are Connect methods implemented in Go.
- A browser mode, off by default: `djinn up --browser` serves the same interface and API on
  127.0.0.1, on a free port, behind a token drawn at launch. It is the end-to-end test target.
- A dev mode where the Go side and the React side reload while you use the app.
- The interface keeps calling `window.djinn`: a shim maps its calls onto the generated
  clients, so the React code does not change at first.

- The native window is in every build by default; `-tags headless` drops it for tests and CI
  (no CGO). On Ubuntu 22.04, add `-tags gtk3` (the build task does it).
- **No port on macOS and Linux**: the window is served by the Wails internal asset server
  (`wails://`, a stream is proven to arrive value by value on WebKitGTK) and the command line uses
  a Unix socket. **Windows** uses the loopback HTTP server with a token, because WebView2 buffers
  custom-scheme responses. `--browser` uses HTTP everywhere. `go tool task check-window` measures
  the path of each system (on Linux: median latency under 1 ms, p90 under 2 ms).

## Done when
- [ ] `djinn up` opens the window on Linux and macOS, rendering compared. (needs: a Mac, and a person to compare
  the rendering with Linux)
- [x] A server stream reaches the window under WebKitGTK (`check-window`, 08/10).
- [ ] Same under WKWebView (macOS) and WebView2 (Windows). (needs: `go tool task check-window` on a Mac and on
  Windows)
- [x] `djinn up --browser` serves the interface; `task e2e` drives it. (`go tool task e2e`, 08/10: 13 Playwright
  specs pass against `djinn up --browser`, started by `e2e/global-setup.ts`)
- [x] The interface starts, adds a project and finds it again after a restart. (08/10: `djinn project add`, stop
  `djinn up` by its PID, start it again, `djinn project list` finds it; the window adds through
  `ProjectService.Add` in `src/wish-dialogs.tsx`; `TestProjectAdd`)
- [ ] Editing React or Go during `task dev` shows up without restarting by hand. (needs: a `dev` task, which
  `Taskfile.yml` does not have yet: an agent can write it, a person checks the reload in the window)

## Open questions
- Ubuntu 22.04 builds with the `gtk3` tag, which Wails drops in v3.1. When do we move to GTK 4? *Recommendation: before upgrading Wails past v3.0.x.*

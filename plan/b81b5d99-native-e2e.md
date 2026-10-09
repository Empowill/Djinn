---
id: 01a1184f-cf1a-7ae3-a231-dabcb81b5d99
code: T06
phase: 1
status: in-progress
---

# T06 · End-to-end tests on the native window

**Goal.** An agent drives the real native window, not only the browser.

## How it works

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

## Done when

- [x] One end-to-end scenario runs against the native window on Linux: `go tool task e2e-native`, about 3 s once
      built (Ubuntu 22.04, GTK 3, WebKitGTK 2.50).
- [ ] The same scenario runs on macOS. (needs: a Mac, `go tool task e2e-native` once)

## Decided along the way

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

## What macOS and Windows need

- **macOS.** Run `go tool task e2e-native` once on a Mac. Expected to pass as is: WKWebView takes the same "ready"
  message, the page comes from the asset server too, and the close check expects a hidden window there. The
  temporary folder sits under `/tmp`, so the socket path stays short.
- **Windows.** Not yet. The window loads the loopback HTTP server, not the asset server, so `windowAssets` never
  sees the pages: the ready script and the CSP change must move to that server, behind the same tag. The terminal
  check skips (`whoami` prints the domain), and the test kills the variant (no interrupt on Windows).

## Open questions

None.

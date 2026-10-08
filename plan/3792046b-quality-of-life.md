---
id: 01a1184f-cf1d-7255-b174-2c913792046b
code: T09
phase: 3
status: in-progress
---

# T09 · Quality of life and clean-up

Delegable, not needed to start testing. Given to Djinn itself once phase 2 is done.

- [x] The lead's terminal pinned at the bottom of the flight plan: moved to T21, in phase 2. (done in T21;
  e2e "the terminal shows at the bottom and runs a command")
- [ ] An inbox for instructions added while an agent works, with acknowledgements. (needs: an agent; workers
  take `Send`, no service exposes it)
- [ ] **Djinn's own icon in the system.** The logo shown in the window is also the app's icon:
  the dock, the task bar, the window switcher. Linux: an icon and a `.desktop` file in the
  user's folders (no sudo). macOS and Windows: the icon embedded in the build. Simple, tested on
  each system.
  - [x] One drawing, `build/icon.png`; `tools/icons` makes every size from it (`go run ./tools/icons gen`
    for `build/icon.ico`, `icon.icns`, `icon-256.png`; a test checks they follow the drawing).
  - [x] Linux: `go tool task install` puts `hicolor/<size>/apps/djinn.png` (16 to 512) and
    `applications/djinn.desktop` (`Exec=<binary> up`, `StartupWMClass=djinn`, the window's `WM_CLASS`
    `djinn, Djinn`) in `$XDG_DATA_HOME`. An install elsewhere (`TO=`) leaves them alone, unless
    `DESKTOP=yes`. `desktop-file-validate` passes; GTK finds the icon at every size.
  - [x] Linux window: it had no icon. GTK 3 silently drops a window icon that does not fit one X11
    request (512 px or more), and the app gave 1024 px. The window gets `icon-256.png`.
  - [ ] Seen in GNOME's Activities and dock after an install (by hand). (needs: a person on GNOME)
  - [ ] Windows: the release build makes the `.exe` icon resource (`go-winres`, from
    `build/icon.ico`, into `cmd/djinn/*.syso`, not committed). Built here for amd64; to check by hand
    in Explorer and the task bar on a real Windows. (needs: a Windows machine)
  - [ ] macOS: the Dock shows the icon at run time; Finder and Launchpad need an `.app` bundle with
    `build/icon.icns` (T19). (needs: a Mac, and the bundle of T19)
- [ ] **The page keeps your place.** When something above what you are reading changes (a
  question answered and removed, a section added), the window stays on what you read: it never
  jumps up. Browsers do it by scroll anchoring; WebKit, the engine of the window on macOS and
  Linux, may lack it (to check), so a small script keeps the element in view as a fallback.
  An end-to-end test reads a question halfway down, removes one above, and checks that the
  question did not move on screen.
  - [x] WebKitGTK 2.50 (Ubuntu 22.04) ships `overflow-anchor` switched off (`CSSScrollAnchoringEnabled`,
    "testable"); WebKit's sources now ship it on. `src/scroll-anchor.ts` does it where
    `CSS.supports("overflow-anchor", "auto")` is false, on the wish's scroller.
  - [x] `e2e/wish-scroll.spec.ts`: a question out of sight above the one read opens, then is answered;
    the one read does not move by more than 1 px, any frame. Twice: Chromium's anchoring, and the script.
  - [ ] Checked in the window, on Linux and macOS (by hand). (needs: a person, on Linux and on a Mac)
- [x] MCP, as a thin layer over the command line. (`djinn mcp`, `internal/cli/mcp.go`: stdio, no library; one tool
  per public unary method, 32, its input schema from the request, read and checked by the command line's code;
  `TestMCPListTools`, `TestMCPCallTool`; tried by hand on a `djinn up --browser` in a temporary DJINN_HOME:
  `project_add` then `project_list`. The three streams stay on the command line; see
  [the convention](../docs/cli-convention.md#mcp))
- [ ] Native notifications and a global shortcut. (needs: an agent; `NotifyQuestion` shows nothing yet)
- [x] OpenAPI documentation of the public methods. (`docs/openapi.json`, OpenAPI 3.1, written by `go tool task gen`
  through `tools/openapi`; `TestOpenAPIIsFresh`, `TestOpenAPI`; Redocly lint: valid, one warning on the localhost
  server; its `ProjectService/List` answered a curl with the bearer token)
- [ ] Analytics with DuckDB: who uses what, for how long. (needs: an agent, after a decision on what to collect)
- [x] Remove Electron and the code it no longer needs. (`electron/` is gone, be8f52c; 845461b: the CI no longer
  sets `ELECTRON_SKIP_BINARY_DOWNLOAD`, `docs/agent-protocol.md` describes the command line and the task events,
  the user guide launches `djinn up`; `git grep -i electron` finds only `plan/` history, the license's
  "electronic" and `electron-to-chromium`, which `browserslist` needs to build)
- [ ] CPU limits per worker on Linux (systemd delegation). (needs: an agent, on a systemd Linux)
- [ ] Shared team settings versioned in the repository. (needs: an agent; only `.agents/permissions.txtpb` exists)
- [ ] macOS specifics: no cgroups, pause by signal. (needs: an agent, then a Mac to check; pause by signal is built
  for Linux and macOS alike in T07, `internal/harness/process_unix.go`, and tested on Linux only)
- [ ] Later, after v1: trusted machines and distributed work, see T15. (needs: T15)

---
id: 01a1184f-cf1d-7255-b174-2c913792046b
code: T09
phase: 3
status: open
---

# T09 · Quality of life and clean-up

Delegable, not needed to start testing. Given to Djinn itself once phase 2 is done.

- [ ] The lead's terminal pinned at the bottom of the flight plan: moved to T21, in phase 2.
- [ ] An inbox for instructions added while an agent works, with acknowledgements.
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
  - [ ] Seen in GNOME's Activities and dock after an install (by hand).
  - [ ] Windows: the `.exe` icon in Explorer needs a resource (`.syso`, icon ID 3, made from
    `build/icon.ico` by `winres`) in `cmd/djinn`; Wails then uses it for the window too. Today the
    window and task bar take the PNG at run time.
  - [ ] macOS: the Dock shows the icon at run time; Finder and Launchpad need an `.app` bundle with
    `build/icon.icns` (T19).
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
  - [ ] Checked in the window, on Linux and macOS (by hand).
- [ ] MCP, as a thin layer over the command line.
- [ ] Native notifications and a global shortcut.
- [ ] OpenAPI documentation of the public methods.
- [ ] Analytics with DuckDB: who uses what, for how long.
- [ ] Remove Electron and the code it no longer needs.
- [ ] CPU limits per worker on Linux (systemd delegation).
- [ ] Shared team settings versioned in the repository.
- [ ] macOS specifics: no cgroups, pause by signal.
- [ ] Later, after v1: trusted machines and distributed work, see T15.

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
- [x] An inbox for instructions added while an agent works, with acknowledgements: `djinn task send <task> "…"`
  (`TaskService.Send`), or the box under a running task's events in the window. The message is a `MESSAGE` event of
  the task, journaled as the command; the first text or tool call of the worker after it is preceded by a
  `RECEIVED` event. A worker that ends first never acknowledges it. (`TestSendToRunningWorker`,
  `TestSendUnacknowledged`, `TestSendRefused` with the fake provider; e2e `task-send.spec.ts`)
  - [ ] What "received" proves with a real agent: the agent said something after the message reached its input,
    not that the model read it. Claude may answer it only at the end of its turn. (needs: a recorded claude stream
    with a message sent mid-turn)
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
- [ ] MCP, as a thin layer over the command line. (needs: an agent)
- [ ] Native notifications and a global shortcut.
  - [x] A question asked in an active wish shows a system notification (Wails' notification service): the wish
    and the question's code as title, its text and options as body. A question of a paused wish, an imported one
    (older than a minute) or a decision shows none; each question shows once. (`TestNoticesShowTheQuestionsOfActiveWishes`,
    `TestNoticesTranslateAndClip`, `TestNoticesWithoutNotifier`, with a fake notifier)
  - [x] A click brings the window forward on the wish; a button answers: Yes, or A to D. Linux supports buttons
    (D-Bus actions; no reply field). An answer goes through the server, as the window's would; one refused shows
    the wish. (`TestNoticesRespond`; `TestAnswerFromANotification` on a real `djinn up`)
  - [x] Off in headless builds and with `--browser`: no notifier is set. A system that cannot show them (no session
    bus, macOS without a bundle) leaves them off and the window opens. `UiService.NotifyQuestion`, never called,
    is gone. (`window_none.go`; `noticeService.ServiceStartup` returns no error)
  - [ ] Seen on GNOME, with a click and a button (by hand). (needs: a person on Linux)
  - [ ] macOS: needs the `.app` bundle and its identifier. (needs: T19, then a Mac)
  - [ ] A global shortcut. (needs: an agent)
- [ ] OpenAPI documentation of the public methods. (needs: an agent)
- [ ] Analytics with DuckDB: who uses what, for how long. (needs: an agent, after a decision on what to collect)
- [ ] Remove Electron and the code it no longer needs. (`electron/` is gone, be8f52c; needs: an agent, for
  `ELECTRON_SKIP_BINARY_DOWNLOAD` in `.github/workflows/ci.yml` and the Electron passages of
  `docs/agent-protocol.md`)
- [ ] CPU limits per worker on Linux (systemd delegation). (needs: an agent, on a systemd Linux)
- [ ] Shared team settings versioned in the repository. (needs: an agent; only `.agents/permissions.txtpb` exists)
- [ ] macOS specifics: no cgroups, pause by signal. (needs: an agent, then a Mac to check)
- [ ] Later, after v1: trusted machines and distributed work, see T15. (needs: T15)

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
    `build/icon.icns` (T19). The release builds it: `djinn_darwin_universal_app.zip`, `Djinn.app` with
    `Contents/Resources/djinn.icns` (`tools/macapp`). (needs: a Mac, to open it from Finder)
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
  per public unary method, 41, its input schema from the request, read and checked by the command line's code;
  `TestMCPListTools`, `TestMCPCallTool`; tried by hand on a `djinn up --browser` in a temporary DJINN_HOME:
  `project_add` then `project_list`. The three streams stay on the command line; see
  [the convention](../docs/cli-convention.md#mcp))
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
  - [ ] macOS: needs the `.app` bundle and its identifier. T19 builds `Djinn.app`, identifier
    `io.github.empowill.djinn`; Wails asks for a signed app too, and the bundle is unsigned. (needs: a Mac, to see
    whether the ad hoc signature suffices)
  - [ ] A global shortcut.
    - [x] A chord anywhere on the desktop brings the window forward on the active wish with the newest question
      that waits for you (not one being investigated), else on the flight plan. Through Wails beta.28's
      `app.GlobalShortcut`, taken once the app runs (`events.Common.ApplicationStarted`), so that a refusal is
      known at once; off in headless builds and with `--browser`. (`internal/ui/shortcut.go`;
      `TestShortcutShowsWhatWaits`, `TestShortcutsSet`, `TestShortcutsRefused` with a fake registrar)
    - [x] Configurable in the settings: one field, empty for off, saved on Enter (`UiService.SetShortcut`, kept in
      `settings.json` of the data directory as `ui.v1.Settings`). A chord needs Ctrl, Alt, Cmd or Super and one key
      (a letter, a digit, F1 to F12, Space), so it never takes a key from typing. One the system refuses is kept,
      and the settings say why. (`TestNormalizeChord`, `TestShortcutsSettingsFile`, `TestSetShortcut`; screen test
      "the global shortcut is a field of the settings")
    - [x] The default chord: `Ctrl+Alt+Space` on Linux and Windows, `Ctrl+Cmd+J` on macOS. Checked here, on
      Ubuntu 22.04 with GNOME 42 on X11, by reading every key binding of `gsettings`: `Ctrl+Alt+D`, `Ctrl+Alt+L`
      and `Ctrl+Alt+T` are taken, `Ctrl+Alt+Space` is free (only `Alt+Space` and `Super+Space` are bound).
      Supposed, not checked: KDE Plasma binds no `Ctrl+Alt+Space`; macOS keeps `Ctrl+Option+Space` for VoiceOver
      and `Cmd+Option+Space` for Finder, and binds no `Ctrl+Cmd+J`; on Windows, AltGr is Ctrl+Alt, and no common
      layout types a character with AltGr+Space.
    - What each system does, as Wails' sources say (beta.28, `pkg/application/global_shortcut_*.go`):
      - Linux on X11: `XGrabKey` on the root window, for every state of Caps Lock and Num Lock. A chord another
        app holds is refused at once. Supposed to work; not pressed on a real desktop.
      - Linux on Wayland (`XDG_SESSION_TYPE=wayland`): the `org.freedesktop.portal.GlobalShortcuts` portal. The chord
        is only a preference: the desktop asks the user, and may bind other keys. The portal answers later; its
        refusal reaches the settings through Wails' error handler. Supposed: GNOME has the portal from GNOME 48
        (none on Ubuntu 22.04's GNOME 42: the settings then say why it does not work), KDE Plasma from 5.27; the
        desktop may ask again at each start, as Djinn opens a new portal session each time.
      - macOS: Carbon's `RegisterEventHotKey`; needs no accessibility permission. Supposed; not built on a Mac.
      - Windows: `RegisterHotKey`; a chord another app holds is refused at once. Supposed; built (`GOOS=windows go
        vet`), not run.
    - [ ] Pressed by hand on GNOME on X11: the window comes forward on the wish with the newest open question, then
      on the flight plan once it is answered; a chord another app holds says so in the settings; turned off, the
      chord does nothing. A development build next to the installed Djinn finds `Ctrl+Alt+Space` taken by it: that
      is the refusal case; choose another chord in its settings to try a press. (needs: a person on Linux, with an
      installed Djinn)
    - [ ] Pressed by hand on a Wayland desktop with the portal (GNOME 48 or later, or KDE Plasma): the desktop asks,
      then the chord brings the window forward. (needs: a person on a Wayland desktop with the portal)
    - [ ] Pressed by hand on macOS and on Windows. (needs: a Mac, and a Windows machine)
- [x] OpenAPI documentation of the public methods. (`docs/openapi.json`, OpenAPI 3.1, written by `go tool task gen`
  through `tools/openapi`; `TestOpenAPIIsFresh`, `TestOpenAPI`; Redocly lint: valid, one warning on the localhost
  server; its `ProjectService/List` answered a curl with the bearer token)
- [x] Read-only methods say so. Each public method that answers once says `idempotency_level = NO_SIDE_EFFECTS`
  (12 reads) or `(djinn.v1.writes)` = `WRITES_CHANGE` (23) or `WRITES_DELETE` (6); `djinn mcp` sets `readOnlyHint`
  and `destructiveHint` on every tool, OpenAPI marks reads `x-read-only`, Connect serves them as GET.
  (`TestEveryMethodSaysWhatItChanges`, `TestMCPListTools`, `TestOpenAPI`; `TestReadOnlyMethodsAnswerAGet` on a real
  `djinn up`: GET `ProjectService/List` 200, GET `ProjectService/Add` 405;
  [the convention](../docs/cli-convention.md#reads-and-writes))
- [ ] Analytics with DuckDB: who uses what, for how long. (needs: an agent, after a decision on what to collect)
- [x] Remove Electron and the code it no longer needs. (`electron/` is gone, be8f52c; 845461b: the CI no longer
  sets `ELECTRON_SKIP_BINARY_DOWNLOAD`, `docs/agent-protocol.md` describes the command line and the task events,
  the user guide launches `djinn up`; `git grep -i electron` finds only `plan/` history, the license's
  "electronic" and `electron-to-chromium`, which `browserslist` needs to build)
- [ ] CPU limits per worker on Linux (systemd delegation).
  - [x] `djinn up --worker-cpu 150` (or `$DJINN_WORKER_CPU`), off by default: each worker runs under
    `systemd-run --user --scope -p CPUQuota=150% --`, no root; the scope keeps its PID, process group and streams.
    At start, Djinn starts one scope and reads its `cpu.max`: systemd accepts `CPUQuota` and caps nothing when it
    does not give the user the cpu controller, as on Ubuntu 22.04 (systemd 249 delegates memory and pids only).
    Then, or without systemd-run, or off Linux, workers run uncapped and `djinn up` says why. (`TestPrefix` in
    `internal/harness`, `TestCheckQuota` and `TestCPULimit` in `internal/machine`; on this machine `djinn up
    --worker-cpu 150` printed "systemd does not give the cpu controller to your user", and ran)
  - [ ] Checked capping on a systemd that delegates the cpu controller to users. (needs: a machine where it does,
    or a person with root to add `Delegate=cpu cpuset io memory pids` to `user@.service` once)
- [ ] Shared team settings versioned in the repository. (needs: an agent; only `.agents/permissions.txtpb` exists)
- [ ] macOS specifics: no cgroups, pause by signal. (needs: an agent, then a Mac to check; pause by signal is built
  for Linux and macOS alike in T07, `internal/harness/process_unix.go`, and tested on Linux only)
- [ ] Later, after v1: trusted machines and distributed work, see T15. (needs: T15)

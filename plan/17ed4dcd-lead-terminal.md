---
id: 01a118b4-0310-7924-8cee-d87717ed4dcd
code: T21
phase: 2
status: in-progress
---

# T21 · The lead's terminal, inside the app, by voice

**Goal.** Talk to the lead from inside Djinn exactly as in a terminal today, voice dictation
included, so the session that builds Djinn can move into Djinn and keep going.

## Decided
- **A real terminal, pinned at the bottom of the window**: a pseudo-terminal on the Go side, a
  terminal emulator in the interface. The lead runs in it as it would in any terminal
  (`claude --resume <session>` for a wish that recorded one, see T13).
- **Voice is the condition.** The move into Djinn happens only once dictation works in this
  terminal. Claude Code's dictation (`/voice`) records the microphone itself, through its own
  native module, and only needs the keys to arrive: hold mode needs the terminal to pass key
  repeat, tap mode does not.
- **One lead session, one place.** A session open in Djinn's terminal is not open in another
  terminal at the same time.

- **A restart reopens the leads.** When Djinn restarts (an update, a crash), it reopens the
  window and the lead terminals that were open, on the same sessions (`claude --resume`).

## Done when
- [x] `djinn up` shows a terminal at the bottom of the window, running a shell or the lead,
  with resize, colours, copy and paste. Checked in the native window on Linux and in Chromium.
- [ ] `/voice` works in it on Linux and macOS: hold Space (or tap), speak, the text arrives.
  What the terminal owes it is proved: a Space held in the native window on Linux reaches a program
  in raw mode as the system's key repeat (2 s held, 500 ms delay, 33 a second: 50 spaces, exactly
  the count expected), and `TestHeldSpaceArrivesAsRepeats` shows each space arriving on its own,
  30 ms apart. Left: speak to Claude Code itself, on Linux and macOS. (needs: a person with a microphone, on Linux
  and on a Mac)
- [x] Closing and reopening the window finds the terminal and its session where they were, while
  `djinn up` runs (e2e: a reopened page reads the same shell's output again). Closing the native
  window stops `djinn up`, so it hangs the terminal up.
- [x] An update restarts Djinn and reopens the terminals that ran, on the same sessions, in the same folders;
  one that cannot start is reported (`TestUpdate`, T12).
- [x] A crash reopens the leads that ran, on their sessions, in their folders; a deliberate Quit reopens nothing
  (`TestCrashReopensTheLeads`: a test djinn killed by its PID with SIGKILL, then started again, runs the fake
  `claude --resume <session>` in its folder and shows it; stopped with SIGTERM, the note is gone and the next one starts
  no claude; `TestChangedFollowsWhatRuns`).
- [x] Every start resumes the lead of the first active wish, as `djinn wish resume` does (its provider's resume
  line, in its folder, never the home folder), unless a lead came back already: from a menu, after a Quit, after
  an update whose lead was in another terminal. (`cmd/djinn/firstlead.go`; `TestCrashReopensTheLeads`, its last
  step)
- [x] An answer given in the window reaches the lead: one line typed in its terminal, then Enter, once the person is
  quiet, in order; a lead that does not run is reopened on its session (`TestSayTypesOneLineAndEnter`,
  `TestSayWaitsWhileThePersonTypes`, `TestSayWaitsForAProgramJustStarted`, `TestAnswerReachesTheLead`, e2e
  `lead-tell.spec.ts`).
- [x] The window's terminal and every lead open in a project, never in the home folder, where Djinn starts from a
  menu or the Dock: with no project, the terminal asks for one, and a lead does not start (`TestLeadStartsInAProject`,
  `TestWindowTerminalOpensInAProject`, `TestOpenStartsInAFolderGiven`, e2e `terminal-project.spec.ts`).
- [ ] This flight plan's session resumes inside Djinn's terminal and goes on by voice. (needs: a person, by voice)

## Decided along the way
- **Djinn types into the lead's terminal** (`Terminal.Say`): a line, a pause of 300 ms, then Enter, so that an
  agent's prompt does not read them as one paste. It waits until the person has not typed there for 3 s, and a
  program just started until its output paused for 1 s (10 s at most). Sixteen lines wait at most, in order.
  Control characters become spaces: one line, no keys. A lead terminal that runs another program, a shell, gets
  nothing: the line would run as a command.
- **`TerminalService`** (`api/terminal/v1`, all methods internal): `Open` (by name: a running
  terminal of that name is returned, `attached`, else a program starts), `Write`, `Resize`, `Read`
  (server stream of the output from an offset, then live, ending with the exit code), `Close`
  (hang up). Served by `internal/terminal` in `djinn up`, next to the other services.
- **Transport: Connect, no Wails stream.** The output is a server stream, which already reaches the
  window message by message on `wails://` and the browser on loopback HTTP. The keys are small
  unary `Write` requests, sent one at a time by the shim so they keep their order; above 16 waiting
  they leave together. A write takes 1.9 to 2.2 ms round trip in headless Chromium over loopback
  HTTP (e2e, 200 writes), under a millisecond on `wails://` (docs/transport.md): far below the key
  repeat interval (30 ms). `app.HandleStream` would add a second protocol, outside Connect, the
  command line and the browser mode, for a gain nobody can feel.
- **Kept in memory only**: the last mebibyte of output per terminal (a quarter more before it is
  trimmed), enough for a window that reattaches to redraw the screen. Nothing in the database: a
  terminal lives as long as `djinn up`.
- **The program**: the user's `$SHELL` (a login shell on macOS, as Terminal does; PowerShell, else `%COMSPEC%`, on
  Windows), or `djinn up --terminal "<command>"`, run through that shell, in `--terminal-dir <dir>`.
- **Never in the home folder.** Started from a menu, the Dock or Finder, Djinn's working folder is the home folder,
  and a claude typed there asks to trust all of it. Without `--terminal-dir`, the window's terminal opens in the first
  project's folder, in the order the window lists them (`plan.FirstProjectFolder`), asked at each start. With no
  project, it does not start: the window says to create one, with the button that opens "Add a project", and starts
  once one has a folder. A lead's folder follows T13: never the home folder nor one above it (`plan.HoldsHome`); a
  restart does not reopen a lead noted there. It gets Djinn's environment untouched, never
  read, plus `TERM=xterm-256color` and `COLORTERM=truecolor`. On Unix it leads its own session
  with the terminal as controlling terminal (job control works).
- **Stopping**: closing a terminal or stopping `djinn up` sends SIGHUP to the program's group and to
  the terminal's foreground group (a shell's job), and SIGKILL after 3 s. On Windows the
  pseudo-console is closed (CTRL_CLOSE_EVENT), then the process is killed.
- **Libraries**: [creack/pty](https://github.com/creack/pty) (MIT) on macOS and Linux, its master
  side switched to non-blocking so a close ends a pending read;
  [charmbracelet/x/conpty](https://github.com/charmbracelet/x) (MIT) on Windows (ConPTY, Windows
  10 1809+); [xterm.js](https://github.com/xtermjs/xterm.js) 6 (MIT) with its fit and web-links
  addons. All pure Go: `CGO_ENABLED=0` still builds.
- **Interface**: `src/lead-terminal.tsx` wraps the app (`main.tsx`) only when the shim provides
  `window.djinnTerminal`, i.e. when djinn serves the page: the Vite preview and Electron are
  unchanged. Collapsible, height dragged from its top edge (both remembered in `localStorage`),
  a restart button once the program ended. Keys typed in the terminal never reach the app's
  shortcuts (Escape, Ctrl+K…). Copy and paste: Ctrl+Shift+C / Ctrl+Shift+V, Cmd+C / Cmd+V on macOS.

- **Restart**: the running terminals are noted in `restart.json` (name, the exact command line, folder) and run
  again by the new Djinn with that same command: `claude --resume <id>` for a lead, the shell for `main`.
  `terminal.Manager.Running` lists them.
- **After a crash**: the same `restart.json`, with no version and the lead terminals only (named `lead-…`), is kept
  current while `djinn up` runs: `terminal.Config.Changed` rewrites it, atomically, each time a program starts or ends,
  and removes it when no lead runs (`cmd/djinn/crash.go`). A stop as asked removes it: Quit in the tray or the window
  menu, Ctrl+Q, SIGINT or SIGTERM, all of which end `djinn up` the normal way. An update writes its own note in its
  place, which then stays. A crash (SIGKILL, a panic, a power cut) leaves it; so does `djinn up` stopping on an error,
  whose note stops following the terminals before they hang up. The next `djinn up` removes it as it reads it, then
  reopens the leads as after an update; one that does not start is in the banner, its wish keeps the session.
  SIGTERM counts as a deliberate stop, so a session that ends (logout, shutdown) does not reopen the leads.

## Open questions
- macOS asks for microphone permission per app: the lead runs under Djinn, so Djinn needs the
  permission (and `NSMicrophoneUsageDescription` when packaged). Not checked: no Mac at hand.
- Windows: the terminal compiles and is vetted, but has not run on a Windows machine yet; a
  program's exit is seen up to a second late there (the pseudo-console keeps its output open).
- A window that reattaches redraws from the output kept; a long full-screen session that wrote
  more than that may need a redraw nudge (a resize) to repaint cleanly.

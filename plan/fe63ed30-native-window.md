---
id: 01a1184f-cf15-74c4-bd3f-4d72fe63ed30
code: T01
phase: 1
status: in-progress
after: T02 T13
---

# T01 · The native window

**Goal.** Djinn is a native window, the same interface as in the browser, where you talk to the lead, by voice too.

One Go command, `djinn`, opens a native window that embeds the existing React
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
- [x] Editing React or Go during `task dev` shows up without restarting by hand. (08/10, in the browser: headless
  Chromium on the page, `src/wish-app.tsx` edited, the title became "Make a wish · hot" with no reload (a value set
  on `window` survived), status "Live"; `internal/ui/ui.go` edited, devloop printed "Go changed, rebuilding djinn"
  and a new PID, `GetEnvironment` through the page answered the new name; `TestRelay`, `TestChanged`, `TestDevHome`
  in `tools/devloop`)

## Decided along the way
- **`go tool task dev`** (`tools/devloop`): Vite serves the page with hot reload; djinn runs `up --browser`, built
  headless (no CGO: a quick build), and is rebuilt and restarted by its PID when a Go file of the binary changes
  (tests excepted). A relay adds djinn's token to the page's calls, reading djinn's address at each one, so a restart
  needs no reload; like djinn, it refuses a call from another origin. The data lives in `bin/dev-home`, and the loop
  refuses the data folder of the Djinn in use. No native window: the window serves the embedded `dist/`, not Vite.

## Open questions
- Ubuntu 22.04 builds with the `gtk3` tag, which Wails drops in v3.1. When do we move to GTK 4? *Recommendation: before upgrading Wails past v3.0.x.*

## From T03 · Keep the interface working: the `window.djinn` shim

**Goal.** The React interface runs unchanged inside the native window and in the browser.

### Decided
- **The interface moves to the new data all at once** (not screen by screen): it reads wishes,
  tasks, questions, events and blocks from the Go services, in the protos' own shapes. The
  shim's conversion to the old mission model is a bridge for the first imports only, removed
  when the switch lands.
- **Old data is converted once, by the person who has some**: a user with flight plans in the
  Electron format converts them with their own agent (old `djinn-session` file in, `djinn wish
  import` file out). Djinn ships no converter.

### What to do
- Provide `window.djinn` in the page, with the same methods and the same `djinn:event` channel
  as the Electron preload, backed by the generated Connect clients.
- Wire in Go the calls used at start-up and to set up a project; the others answer "not yet",
  cleanly.
- Then migrate the interface screen by screen to the generated clients, and drop the shim.

### Done when
- [x] The interface starts with no error, adds a project and finds it after a restart: it reads the projects from
  `ProjectService`, so a restart finds them in the store.
- [x] ~~No React file changed to get there.~~ Superseded by Q23: B, the switch rewrites the screens on the services.
- [x] The interface reads the Go services, in the protos' shapes, through `src/data/`; `window.djinn`, the shim and
  the legacy bridge are gone (branch `w27-ui-switch`).
- [x] A wish made by the command line shows in the window without a reload; a question answered in the window
  reads as answered by the command line (`e2e/wish-live.spec.ts`).
- [x] The terminal follows the wish shown: its lead while it runs, else the window's terminal (T13; e2e
  `lead-switch.spec.ts`).
- [x] Nothing of the old interface is left: `UiService` keeps only what the window calls (`LoadState`, `SaveState`,
  `ValidateProject` and the never implemented `Watch` are gone, with `state.json`), and every class of `src/*.css` is
  written in some `src/**/*.tsx`, the dynamic families (`tone-*`, `level-*`, `kind-*`) apart (`tests/styles.test.cjs`,
  "every class of the stylesheets is used by a component").
- [x] ~~Clément has reviewed the switch.~~ *Waived by the developer on 10/10/2026: the project is at its very beginning, no review now.*

### The switch

Q23: B. The interface reads the Go services itself, in the protos' shapes, through the generated clients
(`gen/ts/`). The shim and its bridge to the mission model go, at once, when the branch merges.

**Who reads what.** One typed module, `src/data/`, holds the clients, a small store, and the React hooks.

| Screen | Reads | Writes | Live |
| --- | --- | --- | --- |
| Sidebar: projects | `ProjectService.List` | `ProjectService.Add` | `WishService.Watch` (project) |
| Sidebar: wishes by rank, state, three active | `WishService.List` | `Make`, `Move`, `Pause`, `Activate`, `ImportData` | `WishService.Watch` (wish) |
| Wish: header | the wish, its projects | `Grant`, `Pause`, `Activate`, `Export`, `Resume`, `Render` | `WishService.Watch` (wish) |
| Wish: questions, decisions | `QuestionService.List` | `QuestionService.Answer` | `WishService.Watch` (question) |
| Wish: tasks | `TaskService.List` | `TaskService.Stop` | `WishService.Watch` (task) |
| Wish: a task's events | `TaskService.Watch` | | the stream itself |
| Wish: blocks | `BlockService.List`, Markdown | | `WishService.Watch` (block) |
| Wish: rights | `Wish.allowances` | `WishService.Allow` | `WishService.Watch` (wish) |
| Project: skills | `SkillService.List` | | read on open |
| Status bar: machine, gates | `MachineService.Show`, `GateService.List` | | read every few seconds |
| Lead terminal: the wish shown's lead, else the window's | `TerminalService.List` | `Write`, `Resize` | `Read`, `UiService.WatchShow` |
| Update banner | `UiService.WatchUpdate` | `UiService.Update` | the stream itself |

**What stays local.** Display preferences only, in the page's storage: the wish shown, the sidebar folded, the
language, the terminal's height. Nothing of a wish.

**What goes.** The mission model (`types.ts`, `use-djinn.ts`, `workflow.ts`, the mission and step panels), the
missions saved in `state.json` (`UiService.LoadState` and `SaveState` lose their caller), `window.djinn` and
`shim/legacy-bridge.ts`, and the browser preview with demo data: the page needs a djinn.

**Live.** `WishService.Watch` (new) says what changed, never the data: the first message names everything, then
each change of a wish (itself, its tasks, questions, blocks) and of the projects, coalesced. The page reads again
what it shows. A task's events come from `TaskService.Watch`, opened for the tasks on screen. A broken stream
reconnects, and its first message reads everything again.

**Order, in the branch.** Each step is a commit; the merge brings them all.
1. `WishService.Watch`, in Go.
2. `src/data/`: clients, store, hooks, tested against an in-memory server.
3. The new shell on Clément's classes: the sidebar, then the wish view.
4. The lead terminal and the update banner read `src/data/`.
5. The mission model, the shim and their tests go; the end-to-end tests follow the new data.

**Where it stands** (branch `w27-ui-switch`, not merged: Clément reviews it first).

Switched: the side panel (wishes by rank, drag or Alt+Arrow, `n/3`, paused, granted folded; projects), a wish
(questions with options, recommendation and note; tasks with their events live and Stop; decisions; blocks in
Markdown; rights per project; pause, activate, reopen; "My wish is granted"; lead, page, export), make a wish, add a
project, a project's skills, import, settings (agents found, language), the status bar (live, workers, gates held),
the lead terminal, the update banner. The page keeps its place (`scroll-anchor.ts`), and import waits for the
wishes to be read.

Not switched yet, screen by screen:
- **One flight plan for all wishes** (T13): the questions and workers of every active wish in one list. Today, one
  wish at a time.
- **Search and shortcuts** (Ctrl+K palette): gone with the mission model; only Ctrl+N stays.
- **A project's setup**: no indexing options. Summoning a skill stays on the command line. The folder is typed in,
  or chosen with "Choose a folder…" in the native window (`UiService.ChooseDirectory`, T13).
- **Notifications** of a new question: shown by the server itself, not by the page (T09).
- **Visualizations** (HTML artifacts in a frame): gone with the artifact workspace.
- **`npm run dev`** has no djinn behind it: a proxy to a dev djinn would bring it back.
- **`go tool task e2e-native`** follows the new screens, but was not run here: it opens a window.

### Decided along the way
- **The watch says what changed, never the data.** `WishService.Watch` sends a wish's id and the kinds that changed
  (wish, task, question, block, project), coalesced every 100 ms, the first message naming everything. The page
  reads again what it shows: deletions, ranks and readiness come out right because the lamp computes them on every
  read. A task's or question's change also names the wish: readiness follows them. Public: `djinn wish watch`.
- **A task's events** come from `TaskService.Watch`, opened only for a task unfolded on screen, and followed again
  when its status changes (a worker started again). The page keeps the last 2,000 events of a task.
- **Import waits for the wishes to be read**: the button is disabled until then (W26's lost import).
- **Question cards drop Motion's `layout`**: its transforms fought the place-keeping script.
- **A question opens unfolded when it is the only one waiting**; with several, each opens on a click. Its first
  option is chosen beforehand, as before.
- **Mermaid draws in a frame of the page's own origin** (W73). A blob: frame inherited the page's policy, which
  forbids inline scripts: the frame stayed empty in the browser. The build now writes `mermaid-frame.html`
  (`src/mermaid-frame.ts`), served like any page by the server and by the window's asset server (wails://, the same
  handler). Its own policy runs its two scripts by their SHA-256 hashes; the page allows `frame-src 'self'` and keeps
  no `unsafe-inline` for scripts. Sandboxed with scripts only, the frame has an opaque origin and takes the source
  and the theme by message. E2e `theme.spec.ts`: an SVG with its three nodes, dark and light.
- **The Mermaid frame, checked in the window and cached** (W79). `go tool task e2e-native` puts a Mermaid block on a
  wish and waits for "Preparing the diagram…" to go with no error: the frame posts its height only once drawn, so
  WebKit is checked too. The server gives each file of the interface an ETag, the hash of its content, with
  `Cache-Control: no-cache`: a second diagram gets a 304, not the 3.6 MB again (`TestAssetsCached`). The frame's
  policy is unchanged. `visualizationDocument`, its policy, its theme (`visualization-theme.json`) and its texts
  served only tests: gone.
- **Electron goes in the same branch** (its own commit): its renderer was `src/`. With it go its runtime, its tests,
  its packaging scripts, `electron` and `electron-builder`, and the texts only it used. The visualization theme
  moves into `src/`.

### Open questions
- Which calls first? *Recommendation: the ones used at start-up and to set up a project (environment, load and save state, pick a folder, validate and index a project, open a link, notify); the others answer "not yet".*

## From T21 · The lead's terminal, inside the app, by voice

**Goal.** Talk to the lead from inside Djinn exactly as in a terminal today, voice dictation
included, so the session that builds Djinn can move into Djinn and keep going.

### Decided
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

### Done when
- [x] `djinn up` shows a terminal at the bottom of the window, running a shell or the lead,
  with resize, colours, copy and paste. Checked in the native window on Linux and in Chromium.
- [x] `/voice` works in it on Linux and macOS: hold Space (or tap), speak, the text arrives. *On Linux, by the developer, who leads this wish by voice in Djinn's terminal (10/10/2026); macOS not checked, waived by the developer.*
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
- [x] A wish without a lead session says so where you answer: the open question says "no lead to tell: your answer
  goes to the workers and waits in the wish's brief" (a converter still takes it, T07), the answered one and its row in
  the Decisions tab say the answer waits in the brief (W52's question, A; `wish.lead.sessionId` empty, as
  `plan.ErrNoLead`). (`a question of a wish without a lead session…` in `tests/screens.test.mjs`, `an answer in a wish
  without a lead session…` in `tests/data-decisions.test.mjs`)
- [x] The window's terminal and every lead open in a project, never in the home folder, where Djinn starts from a
  menu or the Dock: with no project, the terminal asks for one, and a lead does not start (`TestLeadStartsInAProject`,
  `TestWindowTerminalOpensInAProject`, `TestOpenStartsInAFolderGiven`, e2e `terminal-project.spec.ts`).
- [x] The lead's terminal runs a lead of another agent: the arrow beside **Lead** lists claude, codex and antigravity
  as found on this machine (`UiService.GetEnvironment`, `agy` for antigravity), the missing ones disabled with why,
  the recorded lead's marked; picking another starts a new lead of that agent in the lead's terminal, from the same
  first message as every lead: run `djinn wish brief <wish>` and continue (`TestResumeAnotherAgent`,
  `TestEnvironmentProviders`, the screens test of the split button, e2e `lead-agent.spec.ts` with a fake `agy`).
- [x] This flight plan's session resumes inside Djinn's terminal and goes on by voice. (the lead's session 3b4bd887 came back in Djinn's terminal after each update on 09–10/10/2026, and the developer leads it by voice)

### Decided along the way
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
  pseudo-console is closed (CTRL_CLOSE_EVENT), then the program's Job Object is killed: the program and what it
  started.
- **Libraries**: [creack/pty](https://github.com/creack/pty) (MIT) on macOS and Linux, its master
  side switched to non-blocking so a close ends a pending read;
  ConPTY (Windows 10 1809+) written on [golang.org/x/sys/windows](https://github.com/golang/sys) on
  Windows (`internal/terminal/pty_windows.go`); [xterm.js](https://github.com/xtermjs/xterm.js) 6 (MIT) with its fit and web-links
  addons. All pure Go: `CGO_ENABLED=0` still builds.
- **Interface**: `src/lead-terminal.tsx` wraps the app (`main.tsx`) only when the shim provides
  `window.djinnTerminal`, i.e. when djinn serves the page: the Vite preview and Electron are
  unchanged. Collapsible, height dragged from its top edge (both remembered in `localStorage`),
  a restart button once the program ended. Keys typed in the terminal never reach the app's
  shortcuts (Escape, Ctrl+K…). Copy and paste: Ctrl+Shift+C / Ctrl+Shift+V, Cmd+C / Cmd+V on macOS.

- **Any agent takes a wish over from the brief, not from a session.** Every new lead, claude, codex or
  antigravity, gets one first message, the same for all: run `djinn wish brief <wish>`, then continue from what it
  says (`plan.StartLine`). The brief is a status computed by Djinn from the store: the wish's description first (a few
  lines, `djinn wish describe`, edited in the wish's head), its azimas, what runs and waits, its open questions, its
  latest decisions and blocks, the last lead and when a lead last acted (the journal's latest spawn, block, question…
  that names no worker), then the rules. No lead writes a hand-off for the next one: the plan is the hand-off.
  Picking the recorded lead's agent resumes its session; another agent starts a new lead (`WishService.Resume` with
  `provider`). A new claude lead becomes the wish's lead; codex once `djinn wish set-lead` gives its session; an
  antigravity lead cannot be resumed, so the record stays. A terminal that runs a program is never replaced: the note
  says to exit it there.
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

### Open questions
- macOS asks for microphone permission per app: the lead runs under Djinn, so Djinn needs the
  permission (and `NSMicrophoneUsageDescription` when packaged). Not checked: no Mac at hand.
- Windows: the terminal compiles and is vetted, but has not run on a Windows machine yet; a
  program's exit is seen up to a second late there (the pseudo-console keeps its output open).
- A window that reattaches redraws from the output kept; a long full-screen session that wrote
  more than that may need a redraw nudge (a resize) to repaint cleanly.

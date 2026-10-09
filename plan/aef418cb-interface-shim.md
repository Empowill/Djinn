---
id: 01a1184f-cf17-786b-a218-3485aef418cb
code: T03
phase: 1
status: in-progress
after: T01 T02
---

# T03 · Keep the interface working: the `window.djinn` shim

**Goal.** The React interface runs unchanged inside the native window and in the browser.

## Decided
- **The interface moves to the new data all at once** (not screen by screen): it reads wishes,
  tasks, questions, events and blocks from the Go services, in the protos' own shapes. The
  shim's conversion to the old mission model is a bridge for the first imports only, removed
  when the switch lands.
- **Old data is converted once, by the person who has some**: a user with flight plans in the
  Electron format converts them with their own agent (old `djinn-session` file in, `djinn wish
  import` file out). Djinn ships no converter.

## What to do
- Provide `window.djinn` in the page, with the same methods and the same `djinn:event` channel
  as the Electron preload, backed by the generated Connect clients.
- Wire in Go the calls used at start-up and to set up a project; the others answer "not yet",
  cleanly.
- Then migrate the interface screen by screen to the generated clients, and drop the shim.

## Done when
- [x] The interface starts with no error, adds a project and finds it after a restart: it reads the projects from
  `ProjectService`, so a restart finds them in the store.
- [ ] ~~No React file changed to get there.~~ Superseded by Q23: B, the switch rewrites the screens on the services.
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
- [ ] Clément has reviewed the switch. (needs: Clément's review)

## The switch

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

## Decided along the way
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

## Open questions
- Which calls first? *Recommendation: the ones used at start-up and to set up a project (environment, load and save state, pick a folder, validate and index a project, open a link, notify); the others answer "not yet".*

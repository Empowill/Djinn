---
id: 01a1184f-cf17-786b-a218-3485aef418cb
code: T03
phase: 1
status: open
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
- [ ] The interface starts with no error, adds a project and finds it after a restart.
- [ ] No React file changed to get there.

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
| Lead terminal | `TerminalService` | `Write`, `Resize` | `Read`, `UiService.WatchShow` |
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

## Open questions
- Which calls first? *Recommendation: the ones used at start-up and to set up a project (environment, load and save state, pick a folder, validate and index a project, open a link, notify); the others answer "not yet".*

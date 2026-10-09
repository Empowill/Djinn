---
id: 01a11876-6480-7ec2-9833-abd058ae4a59
code: T13
phase: 2
status: in-progress
---

# T13 · Sessions across projects

**Goal.** A piece of work often spans several projects: an application and its infrastructure,
a library and its users. Monorepos are the exception. Djinn follows one session across all the
projects it touches, and a project is not necessarily a Git repository.

## Decided
- **A session is a mission, and we call it a wish.** One concept, spanning projects from the
  start: what you ask Djinn for, and what it works to grant. In code and in English, `Wish`
  (`djinn wish "…"`; "Wish granted" when delivered); in French, « souhait ». "Wish" and "summon"
  (T23) are the only djinn-themed words; the rest stays plain (task, worker, gate).
- **Djinn proposes, you grant.** Djinn never closes a wish. When nothing is left (every task
  finished, no open question, no action waiting for you, no failed task, no gate waiting), it
  proposes: a big, friendly "My wish is granted" button. The wish is granted when you click it.
- **Opening a wish is a moment, not a command.** You say it ("open a new wish: …"); the lead
  recognizes it and opens the wish in Djinn (`djinn wish make`): a short title, the projects it
  touches, the first questions. The new wish shows at once in the flight plan, below the others
  by rank. With three active, a new wish is made paused: it waits, set aside, until you resume it.
- **Djinn is an orchestration layer, kept apart from the projects it works on.** One central
  store for the machine (missions, tasks, questions, journal) and one folder per project for its
  configuration and index, both in Djinn's data folder. Djinn writes nothing into a project and
  commits nothing there, unless a mission explicitly does it as part of its work.
- **Adding a project feels natural.** In the chat, "it is in ~/code/api" or "clone
  github.com/acme/api" is enough: the lead calls `djinn project add <folder>` or
  `djinn project clone <url>`, exactly as a person would. Both index the project in seconds, with
  no model call, and the mission picks it up at once. A folder that is not a Git repository is
  fine.
- **An exported mission carries its plan, questions, decisions and journal**, never code and never
  a secret: code travels through Git. Projects are referenced by name and remote URL, never by a
  local path; on import, Djinn finds each project on the new machine or asks for it.
- **Names are matched without regard to case** (project names, remote URLs, task codes): the
  default macOS file system ignores case, so two names that differ only by case are one name.
- **The interface keeps its "add a project" screen**, simplified: by default it indexes everything
  at once, with nothing to choose. The indexing options open only for someone who asks for them.
  This changes `src/`: reviewed by the interface maintainer.
- **One command takes you back to a wish.** `djinn wish resume <wish>` starts the lead on that
  wish where it stopped: it resumes the lead's own agent session when the wish recorded one (the
  wish keeps the provider and the session ID, never a secret), and otherwise starts a new lead
  from a brief built out of the store (plan, open questions, decisions, journal). The wish's
  page shows this command at the top, so whoever has the page open is one command away.
- **Several wishes at once, in one Djinn, in one flight plan.** The active wishes are ranked by
  hand: drag a wish up or down, and the one on top has priority (the scheduler serves it first,
  T07). Their flight plans merge into one: one list of questions, one list of workers, one
  journal, sorted as usual (blocking first). Every item keeps the wish it comes from, shown on
  it, so that an answer, a task or a block goes back to the right wish. The machine and the
  gates are shared by all wishes; the rank decides who goes first.
- **Three active wishes at most.** It protects the user's attention, not the machine: granting
  every wish at once means the framework was misunderstood. There may be as many wishes as you like:
  the three first are active, the others wait paused, their workers stopped. How many workers run is a separate limit, set by the machine (T17), with one
  orchestrator for all wishes.

## What we want
- **Projects are a registry**, set up once: point at a folder ("it is here"), or ask Djinn to
  clone a repository, and it is indexed in seconds (what to read first, how to test).
- **Sessions are independent of projects.** A session (today's "mission" and its flight plan)
  lists the projects it works on, and can add one along the way.
- **Each task names its project.** In a Git project it gets its own worktree; elsewhere, writers
  are serialized by exclusive write scopes.
- **Several sessions at once**, in the same project or not, each with its own flight plan.
- **Export and import a session**, so a teammate opens it on another machine; projects are
  matched by name or remote URL, and asked for when missing.

## Done when
- [x] One session drives tasks in two projects, one of them not a Git repository. (08/10, fake provider: one wish,
  W1 done in a Git project's worktree, W2 done outside Git once its edit question was answered;
  `TestSpawnInGit`, `TestSpawnOutsideGit`)
- [x] A session exported here imports on another machine and finds or asks for its projects.
- [x] `djinn wish resume <wish>` takes the lead's own session back in the lead's terminal, starting Djinn if it
  does not run, and attaches to it if it already runs.
- [x] Without a lead session, `djinn wish resume` starts a new lead from a brief built out of the store (plan, open
  questions, decisions, journal): `djinn wish brief`, T22.
- [x] Three active wishes at most, as many paused as you like: a new wish beyond three is made paused; resuming one
  when three are active takes the third place and pauses the third wish; moving a paused wish among the three first
  makes it active there. (`TestThreeWishes`, `TestRank`)
- [x] Pausing a wish stops its workers; they resume on their sessions, without spending a resume, once the wish is
  active again. (`TestWishPauseStopsItsWorkers`)
- [x] The window: Pause or Resume at the top of a wish (Reopen once granted), a trash that deletes the wish with its
  tasks, their events, its questions and its blocks after a confirmation; the side panel shows three rows and
  scrolls under the pointer, a paused wish goes first with its button or by dragging it onto an active one.
  (`TestDeleteWish`, `e2e/wish-lifecycle.spec.ts`)
- [x] The active wishes are ranked by hand (`djinn wish move <wish> --to 1`); `plan.ActiveWishes` gives them in
  order, for the scheduler (T07).
- [x] Djinn proposes a ready wish and never grants it: only `djinn wish grant`, or "My wish is granted" in the
  window, does.
- [x] The window ranks the wishes by dragging them (done in `w27-ui-switch`, T03), and shows them in one flight plan:
  the open questions of every active wish, blocking first, what waits, what runs, the latest decisions, each line
  marked with its wish; a question answered there goes to its wish. (branch `w30-ui`: `src/flight-plan.tsx`,
  `tests/data-flight.test.mjs`, the flight plan tests of `tests/screens.test.mjs`, `e2e/flight-plan.spec.ts`: two
  wishes, a question each, one answered from the merged view and read answered by `djinn question list`)
- [x] Each wish shows its own lead: the wish shown brings its lead's terminal (`lead-<wish id>`) while it runs, else
  the window's terminal; the flight plan shows the window's. Switching back finds the same lead, its output kept.
  (`TestServiceList`, `tests/data-terminal.test.mjs`, e2e `lead-switch.spec.ts`: two wishes with a lead each and one
  without, switched from the side panel)
- [ ] Clément has reviewed the flight plan of several wishes: it changes `src/`. (needs: Clément's review)
- [x] Adding a project in the native window, "Choose a folder…" opens the system's folder dialog and fills the field;
  in the browser the button is hidden and the path is typed. (`UiService.ChooseDirectory` on the Wails dialog:
  `TestChooseDirectory`, `TestChooseDirectoryWithoutADialog`, `TestChooseDirectoryOneAtATime` in `internal/ui`; the
  folder field test of `tests/screens.test.mjs`; `e2e/smoke.spec.ts` "adding a project in the browser types the
  folder". The native dialog itself is not opened by a test.)

## Decided along the way
- **The export format is a proto**, `WishExport` in `api/plan/v1`, version 1: the wish, its project
  references (name, remote URL, Git or not), its tasks, their events, its questions, its blocks and
  the commands of the journal that changed it. A `.djinn` file is the binary protobuf; a `.json`
  file is the same in protobuf JSON, for a reader. Nothing of the Electron `djinn-session` format
  is kept, and no converter reads it.
- **What stays on the machine**: a task's worktree and agent session, an event's raw provider line,
  the text of tool results and logs (often code), the input of a tool call (its name stays), and the
  commands that added projects (they hold folders). The folders of the projects and the home folder
  are replaced in every text by the project's name and `~`. A remote URL loses its credentials.
- **Commands**: `djinn wish export <wish> [--file f]` (by default the Downloads folder) and
  `djinn wish import <file> [--replace]`; the window imports through `ImportData`, which also
  journals every import with its content. A wish already here is refused, unless `--replace`.
- **Identifiers are kept** (UUIDv7); an import whose entity ids belong to another wish is refused.
  A task whose worker was running is imported as interrupted.
- **Projects are matched** by remote URL (its HTTPS and SSH forms are one), then by name, case
  ignored; otherwise added without a folder. `djinn project add <folder>` then attaches it, found by
  its remote or its name, instead of adding a second project.
- **The journal follows the wish**: a re-export carries the commands of the first machine, not the
  import itself, so files never nest.

- **The lead is in the lamp**: `Wish.lead` (`Lead`: provider, session ID, folder), set by
  `djinn wish set-lead <wish> <session> [--provider codex] [--directory <folder>]` (the folder defaults to the
  wish's first project; never the home folder, nor a folder above it). Only Claude (`claude --resume <id>`) and Codex (`codex resume <id>`) can be resumed in a
  terminal; the session ID is a word (`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`), so the line goes through the user's
  shell as it is. Never a secret.
- **The lead travels with the wish**: its provider and session ID go in the export; its folder only as a project's
  name or `~` (an absolute folder outside both is left out), and the import puts this machine's folder back when it
  has the project, or leaves it empty for `set-lead`.
- **One Djinn per machine (per data directory), and the commands go through it.** A second `djinn up` asks the
  running one to show its window (`UiService.Show`: restored, raised, focused; in browser mode it prints the page's
  address again) and exits 0. An address file nothing answers is from a crash: `djinn up` starts over it. Our own
  mechanism (the socket or the loopback address of `server.addr`), not the single instance of Wails.
- **`djinn wish resume`** is marked `autostart`: without a djinn, the command line starts `djinn up` detached
  (its own session, output in `djinn.log`), waits for it, then calls it. The server opens the terminal
  `lead-<wish id>` running the resume line in the lead's folder, or attaches to it, and asks the window to show the
  wish and that terminal (`UiService.WatchShow`, replayed for a minute to a window that opens later).
- **A session never runs twice in Djinn**: a lead is not started while another terminal of Djinn runs a command
  line naming its session ID (`--terminal "claude --resume <id>"` included). Djinn does not see a session running
  outside it: close it there first.
- **Closing the window does not quit Djinn**: it is minimised (hidden on macOS); "Quit Djinn" in the tray menu,
  Ctrl+Q (Cmd+Q) in the window, or stopping `djinn up` quits.

- **A wish has a state, in the lamp**: `Wish.state`, active, paused or granted; a wish stored before states is
  active. `djinn wish pause`, `djinn wish activate` (it also reopens a granted wish), `djinn wish make --paused`. A
  wish made or imported beyond three active comes in paused. `djinn wish activate` and `djinn wish move` on a paused
  wish make it active within the three first, pausing the wish pushed past the third place. A paused wish's workers
  stop (`harness.Shelve`): each task waits RESUMING and starts again on its session once the wish is active.
  `djinn wish delete` deletes a wish with its tasks, their events, its questions and its blocks; its workers stop
  first; the worktrees and branches stay. The limit of workers is another one, the machine's (T17).
- **The rank**: `Wish.rank`, from 1, without gaps; 0 for a paused or granted wish. A new or activated wish goes
  last; `djinn wish move <wish> --to n` moves one, and beyond the last means last. An import that replaces a wish
  keeps its place. `djinn wish list` gives the active wishes by rank, then the paused ones, then the granted ones.
  The rank stays on the machine: an export leaves it out.
- **Ready to grant** is computed by the lamp on every read, never stored nor exported (`Wish.ready`): the wish has
  tasks, each one done or stopped by the user, and no open question. A task waiting for an answer, failed,
  interrupted, planned or running keeps it from being ready. Gates do not exist yet (T07): a gate waiting will
  count when they do. A granted wish is never proposed again.
- **Granting is the user's act**: `djinn wish grant <wish>` (`WishService.Grant`), ready or not; granting twice
  changes nothing. The rights a wish gives its workers, `djinn wish grant` until now, are `djinn wish allow`
  (`WishService.Allow`, `Wish.allowances`, `Allowance`), with the same field numbers; `WishServiceGrantRequest`
  reserves the numbers of the old project and mode, so an old journal entry reads as a grant request without them.
- **The window, through the legacy bridge until T03**: an imported mission carries `fromWish`. Its step awaits you
  only when the wish is ready, with "My wish is granted", which calls `Grant`; a granted wish's step is approved.
  A wish's documents no longer make a "Your move" on their own. The bridge reads the readiness at import: the
  mission does not follow the wish afterwards.
- **The wish's page** (`djinn wish render`, `sync`) shows where the wish stands here: active and its rank, paused,
  or granted and when. A ready wish adds a line to "Waiting for you", with `djinn wish grant <wish>`.

- **A new lead from the brief**: `djinn wish brief <wish>` prints it (Djinn's rules and the projects' rules, then
  where the wish stands; no model, no local path). `djinn wish resume <wish> [--provider codex|antigravity]` without a
  lead session starts the agent on it in the wish's first project, else the first of Djinn's projects, never in the
  home folder; with no project nothing starts, and the error says to create one. A session recorded in the home
  folder is not resumed there (claude finds a session only from its folder): a new lead starts the same way. It runs
  claude with the stable part appended to its system prompt and the rest as its first message, its session chosen by Djinn
  and recorded as the lead once the terminal runs; codex and agy with the whole brief as their first message. The
  details, and what is verified or supposed, are in T22.

- **One flight plan, in the window** (`src/flight-plan.tsx`, branch `w30-ui`). An entry above the wishes, "Flight
  plan", with the count of questions waiting. The window opens on it while a wish is active; a wish chosen, or shown
  by `djinn wish resume`, opens its own view, which stays. It reads the tasks, questions and blocks of the active
  wishes (three at most) all the time: the side panel counts the questions waiting in each. Sections, each hidden
  when empty: a card per wish (rank, questions, tasks running, what it spent; a click opens it), "Your move" (the open
  questions of every wish, blocking first, then by the wish's rank; the workers waiting for an answer or cut short;
  the wishes Djinn proposes to grant, with their button), "Running now" (the workers, with Stop and their events),
  "Latest decisions" (eight, the latest first). Every line carries its wish's rank and title; an answer, a stop or a
  grant goes to that wish. A question blocks when a task waits for its answer (`edit_question_id`), as on the wish's
  page.
- **Each wish shows its own lead, in the one terminal.** The window keeps one terminal at the bottom; the wish it
  shows switches it to that wish's lead (`lead-<wish id>`) when it runs, else to the window's own (`main`), and so
  does the flight plan. The window asks `TerminalService.List` (internal: the terminals whose program runs) and
  never opens a lead itself: only `djinn wish resume` and the routing start one. `UiService.WatchShow` still
  switches it, with the wish it shows. A lead that ends while shown stays, with its restart button.
- **The journal stays per wish.** The merged plan has no merged journal: a wish's journal (its commands, from
  `WishService.Snapshot`, and its log blocks) shows in its own view, the commands on a click, since Snapshot reads
  the whole wish.

## Open questions
- A journal of every active wish in the flight plan, merged by time? *Recommendation: not before someone asks;
  Snapshot reads a whole wish, events included, and three of them on every change is heavy. A light read of the
  journal (`WishService.Journal`, the entries only) would come first.*
- `Harness.Recover` marks every pending task interrupted at start-up, so the planned items of an
  imported wish (never started) turn interrupted after a restart. *Recommendation: recover only the
  tasks whose worker started (a start time), in the harness task.*

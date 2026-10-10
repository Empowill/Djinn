---
id: 01a11a06-4f06-793a-9e8a-1e784a699d0f
code: T25
phase: 2
status: in-progress
after: T01 T02
---

# T25 · Understand and decide at a glance

**Goal.** What waits for you comes first, every question reads clearly, and tilasms keep what explains a wish: you
understand and decide at a glance.

The developer's words: "I want to understand what is going on at a glance. Readability, decision and fast action
come first." The window (`src/`) keeps Clément's components and CSS approach, with a new layout and visual language
(`src/review.css`), and the lamp learns what the developer says with a click instead of a word.

**What was decided.**
- **Marks** live on what they mark: `repeated Mark marks` on `Question` (10) and `Block` (11), one mark of each kind
  (read, approved), with who and when. `djinn mark put <Q03|id> read|approved [--remove]`, `djinn mark list <wish>`,
  and a "Marked by the developer" section in the brief. Approving an open question answers it with the option its
  recommendation names (`plan.Recommended`: a letter first, `B: …`, or the first word of one option only).
- **Rounds**: "Enlighten me" (`djinn question enlighten`) and the lead's `djinn question revise` add a `Round` to the
  question (11), the revision bumps `revision` (12). A question whose last round is a request is *being
  investigated*: computed (`plan.Investigating`), not stored, and not counted as waiting for the developer. The
  revision keeps the former context, options and recommendation. The brief lists "To investigate", with the note.
- **Theme**: the system's by default, dark or light in the settings. Every surface takes its colours from the tokens
  of `src/theme.css`: Clément's greys became a neutral ramp (`--n-XX` is `#XXXXXX` in the dark theme), read again on
  a light ground; the terminal's colours (`--term-*`) and Mermaid read the same tokens.
- **Decision log** (W59): a decision is an answered question or a block of kind decision, nothing else; they live in
  a "Decisions" tab, read only. `Question.icon` (15) and `Block.icon` (12), one emoji, set with `--icon` (`💬` and
  `📌` by default; Djinn's own questions carry 🔓, 🧭, 🏁); `Task.decision` (32), set with `djinn task spawn
  --decision` (an answered question's code, or a decision block's id), links a task to its decision. Who decided is
  computed (`render.Decisions`): the developer for an answered question or an approved block (the tone *human*),
  else the worker the block is about, else the lead. Marks stay on open questions and on the other blocks only.
- **Three levels for an open question** (W88, ported by W134; W40's answer A): *blocking* is computed, a waiting task
  needs the answer (`render.UrgencyOf`), red; *before X* is `Question.before` (17), a few words set with `djinn
  question ask --before "before the merge"` and changed with `djinn question revise --before …` (`--before ""` clears
  it), orange under those words; empty, the question *can wait*, grey with a clock (tone `later`). Every list orders
  them blocking, before X, can wait (`render.ByUrgency`, `byUrgency` in `src/data/flight.ts`); in the attention bars
  a question that can wait comes after a worker that asks to edit. Questions asked before have no words: they can
  wait.
- **Two gestures on an open question** (Q52, A): "Enlighten me" on the left digs further; "Rub the lamp", in the
  lamp's yellow, answers with the option selected and the note (`QuestionService.Answer`). The recommended option is
  selected first; picking another changes what the lamp sends; with no option named, the lamp waits for a pick.
  "Confirm the choice" is gone. The window no longer approves a question by a mark; `djinn mark put Q03 approved`
  still does.

## Done when

- [x] A sticky bar at the top of the flight plan and of a wish, only when something waits: one line each, coloured
  by how blocking it is, a click jumps there. (`src/attention.tsx`; e2e `review.spec.ts`; screens "a wish's screen
  puts its questions first…")
- [x] One status language, icon and word, never colour alone: done, running with a live dot, waiting for you,
  investigating, planned, failed, interrupted, stopped, paused; on tasks, wishes in the side panel (questions
  waiting, workers running) and counts in pills. Contrast 4.5:1 at least in both themes. (`src/status.tsx`; test
  "the status colours keep their contrast in the dark and the light theme"; screens "the side panel ranks…")
- [x] Question cards: title, the recommendation boxed first, options as buttons, context with headings below,
  "Enlighten me", "Rub the lamp" (answer with the option selected), a read mark. (screens "a question shows its
  options by letter…"; e2e `review.spec.ts`)
- [x] Read marks and approvals in the lamp, read by the lead with `djinn mark list` and in the brief. (`TestMarks`,
  `TestRecommended`; e2e: Rub the lamp read back with `djinn question list`, a block marked read seen in
  `djinn wish brief`)
- [x] Enlighten and Revise, with their history, in the brief and on the page. (`TestRounds` in `internal/plan` and in
  `internal/render`; e2e: enlighten with a note, read in the brief, revised from the CLI, the revised card rubbed)
- [x] Titles, subtitles, a readable measure; the blocks in Markdown typography; logs, events and rounds as compact
  tables, folded. (screenshots `test-results/e2e/review-*.png`)
- [x] A task is closed by hand, reliably, instead of deleted: `djinn task done <task> [--note …] [--by developer]`
  (`TaskService.Done`) closes a task no worker runs (planned, waiting, cut short, failed, stopped, imported), records
  `Task.closed` (who, when, why), journals it, keeps it through export and import, lets a dependent start; a running,
  paused or done task is refused. A yes to the edit question of a task closed while waiting starts nothing.
  (`TestDone`, `TestDoneStartsDependent`, `TestDoneWaitingThenYes`, `TestDoneCommand`, `TestExportKeepsClosure`)
- [x] The tasks have their own tab, in a wish and in the flight plan: what moves or waits first, by status (running,
  interrupted, failed, then waiting, paused, then planned), then the azimas with the work part of them, the done
  ones folded, with who closed them and why; "Mark done", with an optional note, on any card no worker runs. The page
  and the brief follow the same order. (screens "the Tasks tab lists what moves or waits by status…", "a task no
  worker runs can be marked done…"; data "the Tasks tab: what moves or waits by status…"; `TestFinishedNewestFirst`,
  `TestMovingByStatus`, `TestBriefFinished`; e2e `task-done.spec.ts`)
- [x] A "Decisions" tab in a wish and in the flight plan: the answered questions and the decision blocks, the latest
  first, without a button; who decided, the developer's in the human tone with an icon and a word; one emoji per
  decision; links to the tasks it led to, and from a task to its decision. The page's decision table and the brief
  follow. (`TestIcon`, `TestDecision`, `TestSpawnDecision`, `TestDecisions`, `TestDecisionTable`, `TestContrast`,
  `TestBrief`; data "the decisions are the answered questions and the decision blocks…", "the Decisions tab lists
  them without a button…", "a task links back to the decision it comes from"; "the status colours keep their
  contrast…"; e2e `decision-log.spec.ts`, screens `test-results/e2e/decision-log-*.png`)
- [x] Three levels for an open question: blocking, before X, can wait, in that order on the cards, the attention bar,
  the page and the brief. (`TestBefore`, `TestByUrgency`, `TestBusyPage`, `TestBar`, `TestBrief`, `TestConvention`
  "an optional flag given empty is present"; data "open questions come blocking, then before X, then can wait…";
  screens "a question shows its options…", "the attention bar says how much each question holds up…"; e2e
  `question-levels.spec.ts`, `review.spec.ts`)
- [x] Two gestures only on an open question, wherever the card is drawn: Enlighten on the left, Rub the lamp
  confirming the option selected, the note with it; no "Confirm the choice". (screens "a question shows its options
  by letter…"; e2e `wish-live.spec.ts`: B picked over the recommended A, rubbed, read back as `CHOICE_B` with its
  note; `decision-log.spec.ts`, `flight-plan.spec.ts`, `wish-scroll.spec.ts`; screens
  `test-results/e2e/question-card-dark.png` and `question-card-light.png`)
- [x] ~~Clément has reviewed the layout and the visual language.~~ *Waived by the developer on 10/10/2026: the project is at its very beginning, no review now.*
- [x] The light theme on the dialogs, the agent chat and the terminal, then the system's theme by default. (tokens
  in `src/theme.css`; tests "the theme's tokens keep their contrast in the dark and the light theme" and "the
  stylesheets take their greys from the tokens"; e2e `theme.spec.ts`, screens `test-results/e2e/theme-*.png`)

## From T29 · Tilasms: what explains a wish, kept, linked, opened anywhere

**Goal.** A wish keeps the material that explains it: a concept drawn, a data model walked through, a comparison
laid out. A tilasm (Arabic ṭilasm, the engraved object that holds a power; the origin of "talisman") is a folder with
an `index.html` and its sources, kept on this machine, in its own tab of the wish. It is research material: the
developer and the agents read it, link to it, and a link opens it in the app from anywhere.

**The developer's words.** "A tab with tilasms (artifacts): files or folders with an HTML file and other sources,
saved on the machine, to explain the concepts, exportable and importable. It will be research material, and it must
be referenced by simple links that open it natively in the app from anywhere."

### Decided (09/10/2026, the developer's answers)

- **A tilasm belongs to a wish**: a tab *Tilasms* in the wish, beside Tasks and Decisions. Its code is `L01`, `L02`…
  in the wish (T is the azimas').
- **Stored in Djinn's data folder**: `~/.config/djinn/tilasms/<id>/`, the folder as given (`index.html` and its
  sources) and a manifest (title, wish, author, dates, what it cites). Nothing goes into a project's repository.
- **Two links**: `djinn://tilasm/<id>`, a scheme registered with the system on Linux, macOS and Windows, opens Djinn
  on the tilasm (starting Djinn if it does not run) from a browser, Slack, a terminal or a Markdown file; and a local
  http address of the same tilasm, for a browser or an agent that wants the HTML. `djinn tilasm open <code>` too.
- **Scripts, no network**: a tilasm opens in an isolated frame, as the Mermaid diagrams do (a policy of its own,
  served by Djinn): its own scripts and local files run; no network, no access to Djinn nor its data.
- **The agents and the developer make them**: the lead and the workers with `djinn tilasm put <folder> --wish …`;
  the developer by importing, or dropping a folder or a `.zip` on the tab.
- **Exported with the wish**: `djinn wish export` carries its tilasms, and its import brings them back; a tilasm
  also exports alone, as a `.zip` (the folder and its manifest), and imports into the wish shown.
- **Replaced, with a history**: putting a tilasm again replaces it; its link stays and shows the latest version;
  the earlier versions can be read and restored.
- **Research material**: a `djinn://tilasm/…` link in a block, a question, a decision or the brief opens in the app;
  the brief lists the wish's tilasms; a full-text search in the tab, on titles and text, within the wish;
  `djinn tilasm get <code>` gives an agent the text and the path of the files; a tilasm cites the azimas and tasks it
  explains, and each of them shows its tilasms.

- **"Talisman" counts too**: the word the developer may use, in English and French. `djinn talisman …` is a synonym
  of `djinn tilasm …`, the tab's search finds "talisman", and the lead understands both.
- **The first tilasm** shows the objects stored in the database, drawn from the protos (`api/plan/v1/plan.proto` and
  the others): each entity, its fields and their meaning, and how they link (wish, azima, task, question, block,
  tilasm…). It is made once the tilasms work, and proves them.

### Done when

- [x] `djinn tilasm put|list|get|open|history|restore|export|import` (`TilasmService`), journaled; a put replaces,
  keeping the earlier version. (needs: the tests) — 09/10: `open` came with the links (`TestTilasmOpenAndLinks`: by
  code or identifier, in its wish's Tilasms tab, refused without a window; `get` gives the `djinn://` link and the
  local http address). `TestTilasmPutReplaceHistoryRestore` (put, a put by code is a new version, get with the text, history, restore, the
  journal), `TestTilasmExportImport` (a `.zip` with `tilasm.json`, imported into its wish, another wish, another
  machine), `TestTilasmCeiling` (50 MiB, a field, refused beyond, saying so), `TestTilasmSearch`.
- [x] The *Tilasms* tab of a wish: list, open in an isolated frame (scripts run, the network is refused), search,
  history, export, import by drop. (needs: a screens test and an e2e) — 09/10: `/tilasm/<id>/` serves the latest
  version with `TilasmPolicy` (`TestTilasmFilesServeTheLatestVersionWithTheirPolicy`: `connect-src 'none'`,
  `default-src 'none'`, `sandbox allow-scripts`, a new version at the same address); in the browser the frame's
  files load from an address with a key of their own, the frame's opaque origin carrying no cookie
  (`TestGuardTilasmFrame`); a drop goes through `PutData` (`TestTilasmPutData`: a folder, a `.zip`, an export
  imported). Screens: "the Tilasms tab lists each tilasm…", "a wish's view has a Tilasms tab…", "a folder dropped on
  the Tilasms tab is read…". e2e `tilasms.spec.ts`: put by the CLI, opened in the tab (its script runs, a fetch is
  refused by `connect-src`, the parent page is out of reach), searched ("talisman" too), a version restored. Export
  and a real drop are not clicked in the e2e (export writes to the Downloads folder); the native window is not
  checked yet (`go tool task e2e-native`).
- [ ] `djinn://tilasm/<id>` opens the app on the tilasm from outside, Djinn running or not, on Linux (a desktop entry
  for `x-scheme-handler/djinn`), macOS (`CFBundleURLTypes` in Djinn.app) and Windows (the registry, by the
  installer). (needs: Linux by test; a Mac and a Windows machine by hand) — 09/10, Linux done: the system runs
  `djinn open <link>`, which hands the link to the running Djinn (one per data folder), starting it when none runs
  (`TestOpenHandsTheLinkToTheRunningDjinn`: started then shown, handed over without a second Djinn, an unknown link
  or tilasm said on the command line and in the window, an https link refused); `UiService.OpenLink`
  (`TestOpenLink`), `internal/link` (`TestParse`: `djinn://tilasm/<id>`, `djinn://wish/<id>`, `talisman`, any case, a
  trailing slash, a query); the menu entry of `tools/icons desktop` runs `djinn open %u` with
  `MimeType=x-scheme-handler/djinn;` (`TestDesktopPutsTheIconAndTheEntryInTheUsersFolder`, with
  `desktop-file-validate`), made the handler by `xdg-mime default` (`TestDesktopEntryHandlesTheLinks`, in the test's
  own folders). **macOS and Windows: built and unit-tested here, to check by hand there.** macOS: `CFBundleURLTypes`
  in Djinn.app's Info.plist (`TestBundleLayout`), the link handled through Wails' `ApplicationLaunchedWithUrl`; to
  check: a link starts Djinn.app and shows the tilasm, and reaches a running one; a Djinn started from a terminal is
  not the app macOS knows, and a link then starts Djinn.app, which finds it running and brings it forward without the
  link. Windows: `HKCU\Software\Classes\djinn` (`TestRegistryValues`), written by `go tool task install` (`tools/icons
  desktop`) and by `djinn up` of a release when the user has none (a development build never takes it); to check: a
  link clicked starts Djinn or reaches it, and whether `djinn open` flashes a console.
- [x] Links inside the app (blocks, questions, decisions, the brief) open the tilasm in place. (09/10: Markdown keeps a
  `djinn://` link, which opens the tilasm in its wish's Tilasms tab, or the wish, or says the window does not know it;
  screens "a djinn:// link in Markdown stays a link that opens what it names in place…". The brief keeps the
  link as written, and the page rendered in Go too (`TestPageKeepsDjinnLinks`): opened from there, the system runs
  `djinn open`.)
- [x] `djinn wish export` and import carry the tilasms. (09/10, `TestWishExportCarriesTilasms`: every version's files
  in the export, back on another machine under the same identifiers; the journal keeps the manifests, not the files; a
  replace leaves only the file's tilasms; a deleted wish takes its tilasms' folders.)
- [x] `djinn talisman` answers as `djinn tilasm`, and the search finds "talisman". (09/10, `TestRun`: "talisman
  answers as tilasm", "an alias by a prefix no command takes", from `option (djinn.v1.alias)`; `TestTilasmSearch`:
  "talisman" or "tilasm", in English or French, finds them all, the search the tab will call.)
- [x] The first tilasm, the objects in the database drawn from the protos, is put and opens from its link. (10/10,
  W156: L02, Djinn's data drawn from the protos, in French, `djinn://tilasm/01a123b8-b048-753d-9bee-aaaafa421767`, citing
  T29 and T08. Its ten tables, their fields with the protos' own comments, the messages they hold, the journal, what
  lives outside the database and the enums come from `api/**/*.proto` at `8a2eb67` (`buf build` with source info,
  `plan.Entities()`, `djinn.v1.unique`); seven Mermaid diagrams, Mermaid 11.16.1 copied from `src/vendor`, no network.
  `djinn up` serves it at `/tilasm/<id>/` with `TilasmPolicy`; Chromium renders the seven diagrams under that policy,
  light and dark, no error. It replaces the wish's nine data blocks of 09/10 (eight data notes and a data diagram),
  corrected: `tilasm` and `plugged_source`, 13 new fields on `Task`, 6 on `Wish`, `state.json` gone.)
- [x] The brief lists the wish's tilasms, and the rules tell the lead to make one to explain a concept, and to cite
  it. (09/10, `TestBriefListsTilasms`; the citations both ways, `TestTilasmCitationsBothWays` and `TestSpawnTilasm` for
  `djinn task get|list`; a worker given a tilasm, `djinn task spawn --tilasm`, `TestTilasmContext` and
  `TestSpawnTilasm`)

### Tests must be fast

No real sleep, fake clocks, milliseconds: a test over 1 s is a bug.

### Open questions

- The local http address of a tilasm exists while Djinn serves http (`--browser`, Windows). The window on macOS and
  Linux listens on a socket only, by design (`docs/transport.md`): there `djinn tilasm get` gives the folder of the
  files and the `djinn://` link, no http address. Open a loopback port for the tilasms alone?

- Size: a ceiling per tilasm, 50 MiB (`Tilasm.max_bytes`, decided 09/10); large binary files (videos) are refused past
  it, saying so. A way to raise one tilasm's ceiling is still open.
- A tilasm shared by several wishes: a link from one wish to another's tilasm is enough for now; a library across
  wishes could come later.

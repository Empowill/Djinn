---
id: 01a1184f-cf16-7cc3-9181-dd6ec30479be
code: T02
phase: 1
status: done
---

# T02 · The foundation

**Goal.** Everything starts from the protos: minimal data, a command line generated and documented, everything in
English, clean code.

Everything is generated from `api/`: the server, the interface clients, the command
line agents use, and the public API documentation.

## Decided
- `buf` with the generators as tools of the Go module (`go tool task gen`).
- `api/ui/v1` is reserved to the window; the other packages form the public API.
- Validation with [protovalidate](https://github.com/bufbuild/protovalidate), written once in
  the protos, checked by the CLI before calling and by the server again.
- **The command line follows a convention**, with no CLI option in the protos:
  - command = service without `Service`, then the method, in kebab-case
    (`djinn question answer`); any unambiguous prefix works (`djinn q answer`);
  - `required` fields are positional arguments, in field-number order; other fields are
    `--kebab-case` flags;
  - an enum is typed without its type prefix, case-insensitive (`CHOICE_B` → `b`);
  - a `oneof` of scalars takes one input, stored in the first member whose rule passes;
  - help text comes from the proto comments; `--json` switches the output to JSON.
- MCP later, as a thin layer over the same calls.

## Done when
- [x] `djinn question answer Q03 b` is validated, sent, and printed. (08/10, on a `djinn up` with its own
  `DJINN_HOME`: `djinn question answer Q01 b` prints the question with `answer: choice: b`; `TestRun`)
- [x] A wrong input says, field by field, what was expected. (08/10: `djinn question ask x not-a-uuid` with five
  `--options` prints one line for `--options` and one for `<wish-id>`, exit 2; `TestParseErrors`)
- [x] Adding a method to a proto adds its command, with no hand-written CLI code. (`internal/cli` reads the
  commands from the embedded descriptors; `TestEveryPublicMethodIsExpressible`, `TestEmbeddedDescriptorsAreFresh`)

## Open questions

## Decided along the way
- The command line finds the server through `server.addr` in the data folder (`unix://` on macOS
  and Linux, `http://127.0.0.1:PORT/?token=…` on Windows and with `--browser`); `--addr` and
  `DJINN_ADDR` still force an address.
- A generic runtime by protobuf reflection, no generator of our own: `buf build` keeps the proto
  comments in `gen/djinn.binpb`, embedded in the binary, and a test checks it is fresh.
- Request messages are named `<Service><Method>Request`, so two `List` methods never collide.
- An answered question carries an `Answer` message (choice, note, time): its presence makes the decision.

## From T08 · Data

**Goal.** Store the minimum, in one place, so that any view (window, command line, a peer
later) reads the same thing in real time.

### Decided
- SQLite without CGO (`modernc.org/sqlite`), one writer process, WAL, **one database for the
  machine** in Djinn's data folder: the orchestration layer never lives inside a project.
- **A small ORM written for our protos**, by protobuf reflection: one table per entity, `id`
  (UUIDv7) as primary key, every `*_id` field indexed, the whole message stored as binary,
  only indexed fields as columns. The only annotation: composite unique constraints.
- **Seven entities**: `Project`, `Wish` (what the interface called a mission), `Task`, `Question` (an answered question is a
  decision), `Block` (free content), `Gate`, `Sample`.
- **The command journal**: every request received, stored as is, with its method name. No
  separate event objects.
- Live views follow a stream that resumes from the last sequence number received.
- Short spoken codes (`Q03`, `W2`) are aliases of UUIDs.
- Configuration is protobuf too. Djinn never stores or reads a secret: it may pass one to a
  process, nothing more.

### Done when
- [x] Adding a non-indexed field to an entity needs no migration.
- [x] A command and the entities it changes are written in one transaction.
- [x] A client that reconnects misses nothing and replays nothing. (`tests/data-store.test.mjs`: "a broken watch
  reconnects and reads everything again", "a second follow reads only what is new" by `after_seq`)

### Decided along the way
- **What is built** (`internal/store`, `internal/plan`): `Project`, `Wish`, `Task` and `Question` in
  `api/plan/v1`; `Gate` and `Sample` come with the next phase. `djinn up` opens `djinn.db` in the data
  folder and serves `ProjectService`, `WishService` and `QuestionService` next to the window's.
- **Columns**: `id`, every singular string `*_id` field (indexed), the fields of each `(djinn.v1.unique)`
  group (unique index), and `payload`. Text columns are `COLLATE NOCASE`, so uniqueness and filters ignore
  case. A repeated `*_ids` field (`Wish.project_ids`) is not indexed: a link table waits for a query that
  needs it.
- **Schema from the descriptors at start-up**: tables and columns are added, never dropped. A column added
  to a table with rows is filled from the payloads.
- **The API is short**: `Get`, `List` with an equality filter on indexed fields, and `Tx`, in which `Put`
  refuses to run before `Journal`. Transactions run one at a time (one connection), WAL on the file.
- **The journal** (`command`): `seq`, `id` (UUIDv7), `actor`, `at`, `method` (the Connect procedure) and the
  request in binary. The actor is `local` until agents get an identity.
- **Question codes** (`Q01`, `Q02`…) are given per wish, after the highest one, and never reused since
  questions are never deleted. `answer` takes a code or a UUID; a code used in several wishes needs
  `--wish-id`. The server validates every request against the proto rules, as the command line does.
- **`djinn project add <folder>`**: the command line makes a relative folder absolute (a `directory` field,
  see `docs/cli-convention.md`); the server resolves symbolic links, takes the folder name by default,
  refuses a name or a folder already known (case ignored), and marks a Git repository.

- **`Block`, the free content of a wish** (Q27, recommendation A, applied with the export and
  reversible): structure stays for what Djinn computes (tasks, questions, decisions), and the rest
  is a block: `wish_id`, an optional `task_id`, a free `kind` the lead chooses (decision, section,
  log, diagram, report…), a `title`, a `position`, a `media_type` (Markdown by default) and the
  `content`. A kind the interface does not know is shown as text. `BlockService` has `put`, `list`
  and `delete`. **Positions are integers with gaps** (a new block goes 1000 after the last): they
  read and dictate better than fractional keys, and a wish holds few enough blocks that the day a
  gap runs out, renumbering is one command.
- **Questions carry their context and recommendation** (Markdown), so that an exported question
  keeps what the developer reads to answer it.
- **Projects keep their Git remote**, credentials removed, to be found on another machine.
- **A unique group skips empty values**: two projects without a folder (imported, waiting to be
  attached) do not collide. An index without that clause is replaced at start-up.
- The store gained `Delete` (journaled like `Put`) and `Commands`, which reads the journal.

### Open questions
- Configuration files as text protobuf (`.txtpb`) or TOML? *Recommendation: text protobuf, read straight into the config message and validated, with no conversion layer.*

## From T10 · English everywhere

**Goal.** Djinn is open source: everything in the repository is in English, so that anyone can
read, install and contribute.

### Decided
- **The simplest translation that works in Go and in TypeScript**: one source file in English
  for the whole app, and one file per language next to it (`locales/en.json`,
  `locales/fr.json`), keys shared by the Go server and the interface. No namespaces, no
  translation platform, no generated copies.
- **The model translates.** Whoever adds or changes a text (an agent, most of the time) writes
  the English source and its translations in the same change. A test fails when a key is
  missing in a language.

### What to do
- Translate the README, starting with its top: what Djinn is, the one-line install, the prompt
  for your agent.
- Translate the user guide and the agent protocol in `docs/`.
- Historical notes in `docs/` (version logs, verification reports): translate what is still
  useful, delete the rest.
- Interface strings in `src/`: list them, then decide with the interface maintainer how to
  handle languages. "Mission" becomes "Wish" (French: « souhait »).

### Done when
- [x] The README, the user guide and the agent protocol are in English; the historical notes are
  folded into them or deleted.
- [x] The interface texts go through `locales/` (1261 keys, English and French), with a test that
  fails on a missing or unused key.
- [x] No French left in the repository outside `locales/fr.json`.
  - [x] The text: code, tests, fixtures and docs. (6d3c599: the benchmark payloads of `tools/windowcheck/page/`
    and `internal/server/transport_bench_test.go`, the e2e assertions and a recorded answer are in English;
    `docs/v0.2.1-session-harmonisation.djinn.json` is deleted, nothing read it. French is tested from
    `locales/fr.json` only: `TestFrench` and `TestWishState` in `internal/render`, e2e "in French › the interface
    follows the system's language". `plan/` quotes the French words it decides on, « souhait », « invoquer »)
  - [x] A test keeps it so. (`TestNoFrench` in `locales/french_test.go` reads every file of the repository, tracked
    or new, and fails on a French letter or quote mark, or on two French words on one line; `TestFrenchSigns` checks
    the heuristic. Allowed: `locales/fr.json`, the accent folding of branch names in `internal/harness/worktree.go`
    and its test, the name Clément, and « souhait », « invoquer » in `plan/`. The last French test data,
    `TestNoticesTranslateAndClip` in `internal/ui`, now reads its French from `locales/fr.json`)
  - [x] The two README screenshots, taken in French on the Electron app. (retaken in English, dark and light, on the
    browser build: `go tool task screenshots` runs `e2e/readme.shot.ts` on a demonstration wish imported into a djinn
    of its own, and writes `docs/screenshots/readme/{questions,tasks}-{dark,light}.png`; the README shows the one of
    the reader's theme. `mission.png` and `supports.png` are deleted)
- [x] ~~The interface maintainer has reviewed the keys and the English wording.~~ *Waived by the developer on 10/10/2026: the project is at its very beginning, no review now.*

### Decided along the way
- **One catalog per language, flat keys.** `locales/en.json` is the source, `locales/fr.json` its
  translation; keys are `area.name` in snake case, the area being the screen or component
  (`setup.*`, `timeline.*`, `common.*` for a few shared words). Plurals are `key.one` and
  `key.other`, chosen with `Intl.PluralRules` from a numeric `count`; placeholders are `{name}`.
- **Interface: a 90-line `t()` of our own** (`src/i18n.ts`), no library. The compiler refuses a key
  missing from `en.json` (`TextKey = keyof typeof en`). The language is resolved once at page
  load: the one chosen in Connections & preferences (stored in the browser), else the system's
  (`navigator.languages`, which the native window takes from the OS), else English. Changing it
  reloads the page, so module-level texts are fine.
- **Go: package `locales`** at the root of the module, which embeds the JSON files next to it
  (`go:embed` cannot reach a parent folder). `locales.T(language, key, params)` and
  `locales.Match(tags...)`; no plural until a Go text needs one. The server returns no text to a
  person today, so nothing calls it yet.
- **Tests.** `locales/locales_test.go`: every language has exactly the keys of `en.json` with the
  same placeholders; every key the Go code (`locales.T`) or the interface (`t("…")`) uses exists;
  no key of `en.json` is unused (its quoted name must appear in `src/` or the Go code, so no key
  is built at run time). `tests/i18n.test.cjs` checks parity and the interface's keys for those
  who only run `npm test`.
- **Tests read the interface in English** (superseded: they rendered it in French until 08/10). Node reports
  English, the Playwright config asks for `en-US`. One e2e test asks for `fr-FR`, and the French it expects comes
  from `locales/fr.json`: no French string in a test.
- **"Mission" is "Wish" in English and « souhait » in French** in every text; code identifiers,
  CSS classes, stored data and protocol names (`mission_metadata`, …) keep "mission".
  "Support" (a document a wish produces) is "artifact" in English.
- **Journals with French titles, the stored artifact statuses and the Electron copies** went with the mission
  model and Electron (be8f52c, T03): the interface reads the Go services, whose events have a kind.

## From T09 · Quality of life and clean-up

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
  - [x] A message sent mid-turn no longer leaves a claude worker running forever: claude folds it into the running
    turn and writes one result, so the worker takes `queued_turn_count` as the messages still waiting, and closes
    its input at 0. Without that count (older claude, agy), ten quiet minutes after a result end the wait, with a
    `STATUS`. (`TestCatalog/claude/mid-turn-message`, which hung on the old code; `TestStreamQuietAfterResult`)
- [ ] **Djinn's own icon in the system.** The logo shown in the window is also the app's icon:
  the dock, the task bar, the window switcher. Linux: an icon and a `.desktop` file in the
  user's folders (no sudo). macOS and Windows: the icon embedded in the build. Simple, tested on
  each system.
  - [x] One drawing, `build/icon.png`; `tools/icons` makes every size from it (`go run ./tools/icons gen`
    for `build/icon.ico`, `icon.icns`, `icon-256.png`; a test checks they follow the drawing).
  - [x] The lamp and the smoke, in place of the grey hexagon. `build/icon.svg`, drawn by hand in SVG paths: a brass
    lamp of lines and exact arcs, symmetric about its axis, and violet smoke of free curves, gradients and
    transparency rising from its spout and curling back over it, on the dark rounded square. Inkscape renders it to
    `build/icon.png` at 1024 px (the command is in `tools/icons`), then `icons gen`; looked at 16, 32, 64, 256 and
    1024 px: at 16 the lamp's silhouette carries it. The interface's brand mark is the same file (`src/frame.tsx`).
    Two alternatives wait for a choice: `build/icon-alt-plume.svg` (the smoke rises in a plume, a wisp reaching out)
    and `build/icon-alt-silver.svg` (silver smoke).
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
- [x] Documentation of the command line: every command, generated or written by hand, with its arguments and flags.
  (W143: the HTTP API document and its generation are gone, the protos say the HTTP API; the Command line tab is
  filled in from `cli.Reference` when served, so it cannot lag; `TestReference`, `TestCommands`,
  `TestEveryCommandIsDocumented`)
- [x] A documentation site: `docs/site`, its concepts and its Command line tab, built by `go tool task docs` into
  `bin/docs`, served by `djinn up` at `/docs/`, opened from the settings.
  (`TestBuild`, `TestDocs`, `TestCommands`; `e2e/docs.spec.ts`)
- [x] Read-only methods say so. Each public method that answers once says `idempotency_level = NO_SIDE_EFFECTS`
  (12 reads) or `(djinn.v1.writes)` = `WRITES_CHANGE` (23) or `WRITES_DELETE` (6); `djinn mcp` sets `readOnlyHint`
  and `destructiveHint` on every tool, and each read answers a GET, as Connect serves it.
  (`TestEveryMethodSaysWhatItChanges`, `TestMCPListTools`; `TestReadOnlyMethodsAnswerAGet` on a real
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
    Then, or without systemd-run, or off Linux, workers run uncapped and `djinn up` says why. Since T17 the cap is a
    property of each worker's own scope. (`TestScopePrefix` in `internal/harness`, `TestCheckQuota`, `TestReadProbe`
    and `TestProbeScopes` in `internal/machine`; on this machine `djinn up
    --worker-cpu 150` printed "systemd does not give the cpu controller to your user", and ran)
  - [ ] Checked capping on a systemd that delegates the cpu controller to users. (needs: a machine where it does,
    or a person with root to add `Delegate=cpu cpuset io memory pids` to `user@.service` once)
- [x] Shared team settings versioned in the repository. `.agents/settings.txtpb` (`plan.v1.ProjectSettings`, validated)
  gives the project's workers their default `provider`, `model` and `max_budget_usd`; the developer's own file,
  `projects/<project id>/settings.txtpb` in the data folder, wins setting by setting, a task's flags over both; a file
  that sets the provider sets its model with it. `djinn project show <project>` gives each setting's source
  (`repository`, `developer`, `default`), both paths, and why a file cannot be read; such a file stops the project's
  tasks from spawning, with that message, and Djinn still starts. A field name, a key prefix or a long run of letters
  and digits that looks like a secret is refused, comments included, without repeating it.
  ([docs/team-settings.md](../docs/team-settings.md); `TestReadSettings`, `TestSettingsRefuseSecrets`,
  `TestResolveSettings`, `TestProjectShow` in `internal/plan`, `TestSpawnTakesTheProjectSettings` in
  `internal/harness`; tried on a `djinn up --browser` in a temporary DJINN_HOME: `djinn project show app` gave
  `model: sonnet, source: developer` over the team's `opus`, and listed a developer file with `api_key` under
  `problems`)
  - [x] Warm workers start with the settings: a warm worker's model and budget are the ones Spawn fills from the
    project's files, so a task of a project with settings takes it; when the files change, it is replaced, and a
    project whose tasks go to another agent warms no claude. (`TestWarmTakesTheProjectSettings` in `internal/harness`)
- [x] macOS specifics: no cgroups, pause by signal. Pause by signal is built for Linux and macOS alike in T07
  (`internal/harness/process_unix.go`); macOS runs workers in their process group, no scope (W108). Proved on macOS by
  the CI's macos-15 job, green on 10/10/2026 (run 37997257519): `TestPauseProcess` and `TestPauseResume` run there,
  skipped only on Windows.
- [x] Linters in `go tool task lint`, so in CI: golangci-lint v2.14 (`.golangci.yml`, pinned in
  `tools/golangci/go.mod`, run through `go tool -modfile`) on the headless build and on cmd/djinn with `mcp`, its
  gofmt formatter on every Go file whatever its tags; ESLint 10 (`eslint.config.mjs`: typescript-eslint, @eslint-react,
  the rules of hooks) on `src/`, `e2e/`, `tests/` and the root configs. Each rule left out says why in its config.
  They replace `tools/gofmtcheck`, the two `go vet` lines and `gofmt -w` in `format`; every other tool and task is
  still called. W94 began it; W127 finished it. (`go tool task lint`: 0 issues, about 25 s warm; their findings fixed,
  among them a redirect that `//host` sent off the site, `TestGuardToken`)
- [ ] Later, after v1: trusted machines and distributed work, see T15. (needs: T15)

### Ideas, not decided

From the Wails v3 study (08/10/2026), what Wails offers that Djinn has not taken up; each needs a decision before any
work.
- A tray icon that shows the count of running workers and open questions (today the tray has Show and Quit only;
  GNOME needs an extension for a tray).
- Several windows: a wish or a worker detached into its own window, on a second screen.
- Djinn started when the session opens.
- Add a project by dropping its folder on the window (T13 has the folder picker); whether a dropped folder gives its
  path is not checked on any system.

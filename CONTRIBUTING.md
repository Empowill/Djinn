# Contributing to Djinn

For everyone who works on Djinn, people and agents. [`AGENTS.md`](AGENTS.md) points here.

## The lamp and the smoke

A djinn is smoke from a lamp. You look for the lamp; you find the djinn. Djinn is built the same
way: **a hard lamp, free smoke**.

**The lamp is hard.** What Djinn computes and must never get wrong: wishes, tasks and their
states, questions, decisions, gates, the machine, the journal, the [limits](README.md).
Typed in the protos, enforced by code, tested. No model decides here.

**The smoke is free.** What the lead shapes for each wish: a section opened on the fly, a
diagram, a chart, research notes, a hand-off. Blocks the model writes, shown as written, exported
whole.

- **Empty means hidden.** No "To decide" without a question.
- **The model chooses the smoke.** A new section is a block, never a code change.
- **Smoke hardens only when code needs it.** Then it becomes a proto field, with tests.

## Technical directions

Each one is a decision. Changing one is a discussion first.

- **Simple by default, flexible under the hood.** One command, sensible defaults, nothing to
  configure. Options exist, folded away.
- **What no djinn can grant.** The limits in the [README](README.md) are enforced by code.
  No option turns them off.
- **Every machine.** macOS and Linux first, used to the full. Windows works.
- **No sudo for a user.** Djinn installs, runs and updates in the user's own folders.
  `go install` works everywhere: with CGO you get the native window, without it the browser.
  Contributors may need system packages to build the window. Releases will ship binaries that
  update themselves ([T19](plan/71f9c331-releases.md), [T12](plan/1aa20487-auto-update.md)).
- **Djinn builds Djinn.** We use Djinn to build Djinn. Nothing we build or test may touch the
  Djinn we use. See [below](#djinn-builds-djinn).
- **Portable tasks.** `filepath` and `os.UserConfigDir` in Go, `path` in Node, `{{exeExt}}` in
  `Taskfile.yml`. No Unix-only tool in a task: write a small Go command in `tools/`.
- **Protos are the source of truth.** API, command line, storage and config come from `api/`.
  The command line follows a [convention](docs/cli-convention.md), with no code per command.
- **Nothing is lost.** Every write is a transaction with its journal entry, durable through a
  power cut. Stopping Djinn interrupts workers; it never loses them.
- **No secret, ever.** Djinn may pass one to a process. It never stores, logs or reads one.
- **A measured transport.** How the window, the browser and the CLI reach the server:
  [`docs/transport.md`](docs/transport.md). Measure before changing it.
- **Tests never call a model.** Providers replay recorded streams. Every real case becomes a
  recorded one ([`docs/providers.md`](docs/providers.md)).
- **Cross-agent first.** A project behaves the same whatever the agent. `AGENTS.md` holds the
  instructions, `.agents/` the rules; Djinn translates them for each agent at launch. A file of
  one agent's own (`.claude/settings.json`) follows from `.agents/`, and a test checks it.
- **Auto mode by default.** Agents run in their own auto mode, trusted as their providers
  document it.
- **A worker's rights come from where it runs.** Outside any project: read only. In a project:
  the wish's allowance, then `.agents/`, then the agent's own config. In a folder outside Git with
  none, the worker reads until you say it may edit ([order](docs/providers.md#the-order-of-decision)).
- **Three wishes at a time, never more.** A djinn grants three wishes. It guards your
  attention, not the machine: the machine sets how many workers run.
- **Heavy runs take a gate.** Workers share one machine: tests, e2e, code generation and builds run
  through the running Djinn, one at a time: `djinn gate run <name> -- go tool task test`. `.agents/`
  allows no heavy command directly; light ones (`lint`, `test-pkg` on a package) run as they are.
- **Workers never commit.** A worker edits its worktree; `.agents/` gives it no `git commit` and no
  `git push`. The lead reviews each diff, commits in batches and pushes once: fewer commits, one CI
  run instead of one per worker.
- **Two words from the theme, no more.** You make a *wish*; you *summon* a skill. Everything
  else is plain.
- **Names ignore case.** Two names that differ only by case are one name.
- **English everywhere** in the repository. The interface is translated.
- **Whoever writes a text translates it.** Every text a person reads is a key of
  `locales/en.json` (`t()` in `src/i18n.ts`, `locales.T` in Go). Add the English source and every
  translation in the same change. Keys are flat, `area.name`; plurals are `key.one`, `key.other`.
  Tests fail on a missing or unused key. No translation platform.
- **Build tools stay out of Djinn.** Task, buf and the protoc plugins are module tools, never
  imported by `cmd/djinn`. `go version -m bin/djinn` lists no build tool.
- **Open source, decentralized.** No company's internal names in code or docs. No cloud tracker:
  tasks live in [`plan/`](plan/README.md).

## Djinn builds Djinn

You run Djinn, with a wish and its lead in the terminal. Workers build and test Djinn next to
it. That Djinn must never close, restart, lose focus or receive a command it did not ask for. It
changes only when you click "update". Three separations make it so:

1. **The Djinn you use is installed apart.** `go tool task install` puts it in Go's bin folder,
   with a version. Workers rebuild `bin/djinn`, never your copy.
2. **A development build has its own data.** `bin/djinn` (version `dev`) uses a `djinn-dev`
   folder, so it never finds your Djinn, its socket or its window. Every test uses a temporary
   folder.
3. **Tests touch only what they started.** Stop a process by its PID, never by name. No native
   window unless asked (`check-window` opens one, on demand).

When an update restarts Djinn, it reopens the window and the leads that were open, on the same
sessions. You pick up where you were.

Why: dogfooding is our best test, and only if it is safe. A test that can kill your session
gets switched off.

## Getting set up

To use Djinn, see the [README](README.md). To work on it:

**Prerequisites**
- [Go](https://go.dev/dl/) 1.25+, with CGO for the native window.
- Node.js 22 and npm (`npm ci` once).
- The window uses [Wails v3](https://v3.wails.io) (beta.28):
  - **Linux**: a C compiler, GTK and WebKitGTK headers. Ubuntu 22.04:
    `sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev` (the task adds the
    `gtk3` tag). Newer systems use GTK 4 and `webkitgtk-6.0`.
  - **macOS**: Xcode Command Line Tools (`xcode-select --install`): Apple's C compiler and system
    headers, not the Xcode app. You likely have them: `xcode-select -p`.
  - **Windows**: WebView2, part of Windows 11.
- Task, buf and the protobuf generators come with the Go module.

**Everyday commands** (`go tool task --list` for all)
- `go tool task test`: every test (Go, interface, end-to-end). Parts: `test-go`, `test-ui`, `e2e`;
  `test-pkg -- -run TestX ./cmd/djinn` for some packages.
- `go tool task test-race`: the Go tests under the race detector (needs CGO); `-- <go test arguments>` narrows it.
- `go tool task lint`: every check (protos, Go, types, formatting). `go tool task format` fixes.
- `go tool task gen`: code from the protos, and [`docs/openapi.json`](docs/openapi.json).
- `go tool task build`: the `dev` binary, `bin/djinn`. Then `bin/djinn up`.
- `go tool task dev`: the page reloads as you edit React, djinn rebuilds and restarts as you edit Go; in the browser,
  on its own data (`bin/dev-home`). Open the URL it prints.
- `go tool task install`: the Djinn you use, apart from the one you build, with its icon and menu entry on Linux
  (`~/.local/share`). The running one keeps going and offers
  to restart on it ("Update" in the window, or `djinn update` from your terminal; `--yes` elsewhere), reopening the lead terminals.
- `djinn up --terminal "claude --resume <session>" --terminal-dir <project>`: run that command in
  the window's terminal instead of your shell.
- `djinn wish import plan.djinn` (or "Import a wish" in the window); `djinn project add <folder>`
  for each project it names; `djinn wish export <wish-id>` writes to your Downloads folder.
- `djinn wish sync <wish-id>` renders the wish's page in Go and prints its file, kept up to date while
  `djinn up` runs (delete it to stop); the lead republishes that file. `djinn wish render <wish-id>` writes it once.
- `djinn skill summon app/babysit-mr --into infra` lets infra's workers use a skill of app, without a copy: they
  follow the source. `djinn skill list` shows the skills; `djinn skill unsummon app/babysit-mr --from infra` stops it.
- Three wishes are active at most. `djinn wish pause <wish-id>` and `djinn wish activate <wish-id>` free and take a
  place; `djinn wish move <wish-id> --to 1` gives one priority; `djinn wish grant <wish-id>` says it is done.
  `djinn wish allow <wish-id> --mode edit|auto|none` sets what its workers may do in a project.
- `djinn mcp` serves the commands as MCP tools on stdio, for an agent that speaks MCP (`wish_set_lead` is
  `djinn wish set-lead`; [convention](docs/cli-convention.md#mcp)).
- `djinn backup [--file <archive>]` copies the data folder, even while Djinn runs; `djinn backup restore <archive>`
  puts it back, Djinn stopped ([`docs/backup.md`](docs/backup.md)).
- `djinn wish set-lead <wish-id> <session-id> --directory <folder>` records a wish's lead session;
  `djinn wish resume <wish-id>` shows the wish and resumes its lead in the window's terminal,
  starting Djinn if needed. A second `djinn up` brings the window to the front.
- `djinn wish brief <wish-id>`: the brief a new lead starts from, when `djinn wish resume` finds no session.
  `djinn task spawn … --fork W1` or `--from-lead` starts a worker from a copy of a conversation; `djinn up
  --warm-workers` keeps a claude loaded per project; `go tool task bench-workers` (paid, refuses without consent)
  compares them ([T22](plan/1689571a-fast-workers.md)).
- `djinn task send <task-id> "…"` gives a running worker an instruction (or the box under its events, in the window):
  an event of the task, then "received" once the worker says something after it.
- With the window, a question asked in an active wish shows as a system notification: a click shows the wish, a
  button answers it. On macOS it needs the `.app` bundle (T19); a headless build and `--browser` show none.
- `go tool task check-window`: open a window a few seconds and check that a stream reaches it
  value by value. PASS or FAIL.
- `go tool task e2e-native`: drive the real window end to end through the Wails MCP server (a test build, window
  "Djinn e2e"). Opens a window a few seconds, so it is not in `test` ([T06](plan/b81b5d99-native-e2e.md)).
- Tests and CI build with `-tags headless`: no window, no CGO.

## How we work

- **Tasks live in [`plan/`](plan/README.md)**, one file each. The README checklist is the only
  tracker.
- **Every change is reviewed** by a maintainer.

## Dependencies, and thanks

We keep as few dependencies as we can: each one must earn its place, and its license must allow
it (see [Licenses](#licenses-follow-them-to-the-letter)). Djinn stands on the work of these projects; thank you to all
their contributors.

- Go: [Wails](https://github.com/wailsapp/wails), [Connect](https://github.com/connectrpc/connect-go),
  [protobuf-go](https://github.com/protocolbuffers/protobuf-go),
  [protovalidate-go](https://github.com/bufbuild/protovalidate-go),
  [pty](https://github.com/creack/pty) (the terminal's pseudo-terminal on macOS and Linux),
  [conpty](https://github.com/charmbracelet/x/tree/main/conpty) from Charm (the same on Windows),
  [goldmark](https://github.com/yuin/goldmark) (the Markdown of a wish's page),
  [godbus](https://github.com/godbus/dbus) and [go-toast](https://git.sr.ht/~jackmordaunt/go-toast) (the system
  notifications of the window, through Wails, on Linux and on Windows),
  [x/sys](https://github.com/golang/sys), [x/term](https://github.com/golang/term) (tests),
  [x/mod](https://github.com/golang/mod) (release versions); as module tools,
  [Task](https://github.com/go-task/task) and [buf](https://github.com/bufbuild/buf).
- Interface: [React](https://github.com/react/react),
  [Connect for the web](https://github.com/connectrpc/connect-es) and
  [Protobuf-ES](https://github.com/bufbuild/protobuf-es),
  [Motion](https://github.com/motiondivision/motion),
  [Lucide](https://github.com/lucide-icons/lucide),
  [react-markdown](https://github.com/remarkjs/react-markdown) and
  [remark-gfm](https://github.com/remarkjs/remark-gfm),
  [Thinking Orbs](https://github.com/Jakubantalik/Libraries.dev),
  [xterm.js](https://github.com/xtermjs/xterm.js) and its fit and web-links addons (the terminal),
  [Fontsource](https://github.com/fontsource/font-files) (DM Sans, IBM Plex Mono).
- Build and tests: [Vite](https://github.com/vitejs/vite) and
  [its React plugin](https://github.com/vitejs/vite-plugin-react),
  [TypeScript](https://github.com/microsoft/TypeScript),
  [Playwright](https://github.com/microsoft/playwright),
  [Prettier](https://github.com/prettier/prettier),
  [DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped).
- Adapted code and assets are credited in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

## Licenses: follow them to the letter
Djinn is licensed under Apache-2.0 (`LICENSE`), with its copyright notice in `NOTICE`.

- **Before reusing or adapting third-party code** (a file, a function, a model catalog, an
  algorithm taken from a repository), check its license:
  - MIT, BSD, ISC, Apache-2.0: allowed, with attribution as below.
  - MPL-2.0: allowed only as an unmodified dependency, never copied into our files.
  - GPL, AGPL, LGPL, SSPL, BUSL or no license: do not copy. Read for ideas only, and say so in
    the change description.
- **Cite it in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)**, following that license's own
  conventions: the project name, the source URL and the commit or version, what was adapted, the
  original copyright line, and the full license text when the license requires it (MIT and BSD
  do). Keep a short pointer in a comment at the top of the adapted file.
- **A new dependency** (Go module or npm package) needs a license from the allowed list. Name it
  in the change description.
- Never remove or alter a copyright notice or a license header from third-party code.

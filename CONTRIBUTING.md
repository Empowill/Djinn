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
  update themselves ([T19](plan/71f9c331-releases.md), [T04](plan/929d6a88-getting-started.md)).
- **Djinn builds Djinn.** We use Djinn to build Djinn. Nothing we build or test may touch the
  Djinn we use. See [below](#djinn-builds-djinn).
- **Portable tasks.** `filepath` and `os.UserConfigDir` in Go, `path` in Node, `{{exeExt}}` in
  `Taskfile.yml`. No Unix-only tool in a task: write a small Go command in `tools/`.
- **Protos are the source of truth.** API, command line, storage and config come from `api/`.
  The command line follows a [convention](docs/cli-convention.md), with no code per command.
- **Nothing is lost.** Every write is a transaction with its journal entry, durable through a
  power cut. Stopping Djinn interrupts workers; it never loses them: the next start resumes them by itself, in the
  same task, worktree and session. A worker stopped by its provider's usage limit waits for the reset, then resumes.
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
- **Three wishes at a time, never more.** A djinn grants three wishes: three are active at most,
  and the others wait, set aside, as many as you like. It guards your attention, not the machine:
  the machine sets how many workers run.
- **Heavy runs take a gate.** Workers share one machine: tests, e2e, code generation and builds run
  through the running Djinn, one at a time: `djinn gate run <name> -- go tool task test`. `.agents/`
  allows no heavy command directly; light ones (`lint`, `test-pkg` on a package) run as they are.
- **Workers never commit.** A worker edits its worktree; `.agents/` gives it no `git commit` and no
  `git push`. Djinn commits each task's work as it ends, tested, and pushes the branch on a cadence: one CI
  run per push instead of one per worker. Only the review and correction workers Djinn starts by itself commit, in
  their worktree ([integration](docs/team-settings.md#integration)); Djinn commits nothing blindly.
- **Two words from the theme, no more.** You make a *wish*; you *summon* a skill. Everything
  else is plain.
- **Names ignore case.** Two names that differ only by case are one name.
- **English everywhere** in the repository. The interface is translated.
- **Whoever writes a text translates it.** Every text a person reads is a key of
  `locales/en.json` (`t()` in `src/i18n.ts`, `locales.T` in Go). Add the English source and every
  translation in the same change. Keys are flat, `area.name`; plurals are `key.one`, `key.other`.
  Tests fail on a missing or unused key, and on French outside `locales/fr.json` (`TestNoFrench`). No translation
  platform.
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
sessions; so does the next start after a crash, but not after you quit. At every start, the lead of
the first active wish comes back on its session, in its folder (a project, never your home folder),
unless a lead came back already: claude or codex, whichever leads it. You pick up where you were.

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
  `test-pkg -- -run TestX ./cmd/djinn` for some packages. `test-go` fails on any Go test over 2 s on Linux,
  the reference, and over 15 s elsewhere, where a process costs ten times more and a test over 2 s is only reported
  (`tools/slowtests`): a fake clock, a short tick, a wait on an event, never a sleep that waits for luck.
  - **Windows runs what targets the system.** Linux and macOS run every test; the Windows job sets
    `DJINN_TEST_SYSTEM_ONLY=1`, which skips the tests marked portable: `testx.Portable(t)` first in a Go test
    (`internal/testx`), `portable()` in an end-to-end spec (`e2e/portable.ts`). Mark a test portable when it pays
    for git, processes or a whole `djinn up` to check Djinn's own logic: the integration's merges, corrections,
    reviews and pushes, the scheduler, the plan, questions, permissions (most of `internal/harness`), and specs of
    the interface alone. Never mark one that reaches what differs on Windows: a `_windows.go` file or a
    `runtime.GOOS` branch, processes and their stop, paths and worktrees, the terminal, the command line's files.
    A test that costs nothing stays unmarked: it runs everywhere. Two portable tests stay unmarked as Windows'
    smoke of that logic, end to end: `TestCommitEachTaskAlone` (the integration) and `TestDependsOn` (the
    scheduler). `DJINN_TEST_SYSTEM_ONLY=1 go tool task test` runs on any system what Windows runs.
- `go tool task test-race`: the Go tests under the race detector (needs CGO); `-- <go test arguments>` narrows it.
- `go tool task lint`: every check (protos, Go, types, formatting), as CI runs it. `go tool task format` fixes what
  can be.
  - Go: [golangci-lint](.golangci.yml), pinned in its own module (`tools/golangci/go.mod`): nothing to install. It
    formats (gofmt) and runs vet with the linters, on the headless build and on cmd/djinn with the `mcp` tag.
  - Interface: [ESLint](eslint.config.mjs) (typescript-eslint, @eslint-react, the rules of hooks); `tsc` checks the
    types, Prettier owns the layout.
- `go tool task gen`: code from the protos, and the descriptors the command line embeds.
- `go tool task docs`: the documentation site, its concepts and its command line, into `bin/docs` (open
  `bin/docs/index.html`, offline). Its sources are in [`docs/site`](docs/site); `djinn up` serves it at `/docs/`, and
  the settings open it. The Command line tab has nothing to regenerate by hand: `djinn up` and `go tool task docs`
  fill it from the command tree djinn is built from (`internal/cli`), so a method added to a proto, or a flag, is
  there at once. A command written by hand in `cmd/djinn` takes its entry in `cli.Builtins`, which is its `--help`
  too; `TestEveryCommandIsDocumented` fails on one that runs without it.
- `go tool task build`: the `dev` binary, `bin/djinn`. Then `bin/djinn up`.
- `go tool task dev`: the page reloads as you edit React, djinn rebuilds and restarts as you edit Go; in the browser,
  on its own data (`bin/dev-home`). Open the URL it prints.
- `go tool task install`: the Djinn you use, apart from the one you build, with its icon and menu entry on Linux
  (`~/.local/share`). The running one keeps going and offers
  to restart on it ("Update" in the window, or `djinn update` from your terminal; `--yes` elsewhere), reopening the lead terminals.
  It watches the releases too: with your checkout on main, a newer release installs by itself, the restart waiting
  for your click; on a branch, none installs over your build, and Djinn merges main into the wishes' branches instead
  ([releases of Djinn](docs/team-settings.md#releases-of-djinn-for-those-who-build-it)).
- `djinn up --terminal "claude --resume <session>" --terminal-dir <project>`: run that command in
  the window's terminal instead of your shell.
- `djinn wish import plan.djinn` (or "Import a wish" in the window); `djinn project add <folder>`
  for each project it names; `djinn wish export <wish-id>` writes to your Downloads folder.
- `djinn wish sync <wish-id>` renders the wish's page in Go and prints its file, kept up to date while
  `djinn up` runs (delete it to stop); the lead republishes that file. `djinn wish render <wish-id>` writes it once.
- `djinn skill summon app/babysit-mr --into infra` lets infra's workers use a skill of app, without a copy: they
  follow the source. `djinn skill list` shows the skills; `djinn skill unsummon app/babysit-mr --from infra` stops it.
- An inbox source a project's skill declares runs only once you plug it in, on your machine: `djinn inbox sources`
  lists them, `djinn inbox plug babysit-pr` runs Djinn's own (the pull requests assigned to you or that request your
  review, read with `gh`), `djinn inbox unplug <source>` stops one; the empty inbox does the same with its buttons
  ([T26](plan/e1210c5e-request-routing.md)).
- Three wishes are active at most; the others wait, paused. `djinn wish pause <wish-id>` sets one aside and stops its
  workers; `djinn wish activate <wish-id>` takes it back, its workers resumed, in the last place (three being active,
  the third wish is paused); `djinn wish move <wish-id> --to 1` gives one priority, active or not;
  `djinn wish grant <wish-id>` says it is done; `djinn wish delete <wish-id>` deletes it with its tasks,
  closes its lead's terminal and removes the worktrees with no work of their own (branches all stay).
  `djinn wish allow <wish-id> --mode edit|auto|none` sets what its workers may do in a project.
- `djinn task pause <task-id>` holds a worker where it is and frees its slot; `djinn task resume <task-id>` lets it go
  on; `djinn task stop` works on a paused one. A worker that holds or waits for a gate is not paused. The buttons on
  its card in the window do the same. Not on Windows yet.
- In a project whose settings name its checks ([`docs/team-settings.md`](docs/team-settings.md#checks)), Djinn
  integrates finished work by itself: as each task ends, alone, it merges the task's branch in a worktree of its own
  (made ready by the `setup` command, again when a lock file changes), makes the generated files again on a conflict
  only in them, runs the `commit` checks each through a gate of its name, moves the wish's branch when they pass, and
  removes the task's worktree when clean ([T07](plan/8e8d3d76-orchestrator.md)). It pushes the branch, never forcing,
  at an azima's end or once three tasks are committed and an hour has passed since the last push, once the `push`
  checks pass (red, the push is held, and asked about if they stay red); `--push-mode ask` asks first.
  Djinn's own checks are in [`.agents/settings.txtpb`](.agents/settings.txtpb): the lint at commit, the tests at push.
  `djinn project show <project>` and the window's project view list them with their last runs.
  `djinn wish set-integration <wish-id> --branch feat/x` names the branch. A conflict in code, or a red check, start a
  correction worker on the failed merge, part of the same azima; past `correction_attempts` (2), Djinn asks you. A task
  whose worktree holds changes not committed is not merged, and nothing commits them blindly: a review worker, in that
  worktree, commits what belongs to the task and drops the rest, then its work integrates; past the same attempts,
  Djinn asks you. A task waits for
  its dependencies' work to be committed, and its worktree starts from that branch; once a batch is committed, the
  window proposes to install it (the `install` setting) and restart on it. Djinn keeps the wish's branch up with the
  project's main branch the same way: it fetches main at most hourly, and merges it once main holds a release the
  branch lacks, tested, never rebasing, a conflict going to a correction worker (`main_branch`, `merge_main`,
  `merge_main_minutes`; [keeping up with main](docs/team-settings.md#keeping-up-with-main)).
- `djinn task spawn … --after W1,W2` gives a task what comes before it (`--depends-on`, its former name, still works);
  `--blocks W5` puts the new task before a planned one, W5 waiting for it from the same step, refused once W5 has
  started. `djinn task depend <task-id> --after W1,W2 --also W6=W5` sets what tasks wait for, in place of what they
  had, all or none; the tasks of a wish form a graph without cycle, and one that would close a cycle is refused,
  naming it.
- The plan of a wish is a graph of azimas (`T07`): tasks no worker runs, which work is part of. `djinn task spawn …
  --part-of T07` spawns work in one, `djinn task group <task-id> --part-of T07` moves it, `djinn task spawn <wish>
  --kind azima --title "…"` makes one. `djinn plan sync <wish-id>` reads them from the projects' `plan/*.md` and writes
  each one's `after:` back from the store ([T07](plan/8e8d3d76-orchestrator.md)). The Tasks tab groups work under its
  azima. One whose work is finished and whose file's unchecked "Done when" boxes all say `(needs: …)` awaits its
  proof ("Proof awaited"): a person, a machine, a release or a real model gives it, never a worker.
- `djinn task done <task-id> --note "…"` closes a task no worker runs (planned, cut short, failed, stopped,
  imported) once its work is done, with who closed it and why; "Mark done" on its card does it in the window.
  `djinn task delete` stays for a task made by mistake.
- `djinn task continue <task-id> --prompt "…"` gives a task no worker runs (done, failed, stopped, cut short) a new
  turn of its own session, in its own worktree and branch: the same task, back through the scheduler, its usage
  summed. Refused while it runs, once its worktree is gone, or for an agent that cannot resume a session (Antigravity,
  a watcher). `djinn task spawn --fork` starts another task from a task's context; forking a task cut short, failed or
  stopped closes it, "continued in" the fork.
- `djinn tilasm put <folder> --wish <wish-id> --cites T25 --cites W12` keeps a folder with an `index.html` (or a
  `.zip` of one) as a tilasm of the wish, `L01`, `L02`…: the material that explains it, in the data folder
  (`tilasms/<id>/v<n>/`), never in a project; `--code L01` puts a new version of it. `djinn tilasm get L01` gives an
  agent its text and the folder of its files; `history`, `restore <version>`, `export` (a `.zip` with `tilasm.json`)
  and `import <zip> --wish <wish-id>` follow, and `djinn wish export` carries them. A tilasm holds 50 MiB at most.
  `djinn talisman …` is the same command ([T25](plan/4a699d0f-review-at-a-glance.md)). Djinn serves each one's latest version at
  `/tilasm/<id>/`, with a content security policy of its own: its scripts and files run, the network and Djinn stay
  out of reach. The wish's *Tilasms* tab lists them, opens one in a sandboxed frame, searches their titles and text,
  restores a version and exports one; a folder or a `.zip` dropped on it becomes a tilasm. The brief lists the wish's
  tilasms, each with its link, `djinn://tilasm/<id>`, and the azimas and tasks it cites; `djinn task get` and the
  brief's azima graph show each task the tilasms that cite it. `djinn task spawn … --tilasm L01` opens the worker's
  first prompt with the tilasm's text and the folder of its files.
- A tilasm's link, `djinn://tilasm/<id>` (`djinn tilasm get` gives it, and its local http address while Djinn serves
  http: `--browser`, Windows), opens the app on it from a browser, a chat, a terminal or a Markdown file; so does
  `djinn://wish/<id>`. The system runs `djinn open <link>`, which hands it to the running Djinn, starting it when none
  runs; the window shows the wish's *Tilasms* tab on that tilasm, or says it does not know the link. `djinn tilasm
  open L01` does the same. Inside the app, such a link in a block, a question, a decision or the brief opens in place.
  The scheme is registered without sudo: on Linux `go tool task install` adds it to the menu entry
  (`MimeType=x-scheme-handler/djinn`, `xdg-mime default`); on macOS `Djinn.app` declares it (`CFBundleURLTypes`);
  on Windows the install, or else `djinn up`, writes it in the user's registry (`HKCU\Software\Classes\djinn`).
- `djinn mcp` serves the commands as MCP tools on stdio, for an agent that speaks MCP (`wish_set_lead` is
  `djinn wish set-lead`; [convention](docs/cli-convention.md#mcp)).
- `djinn gate run <name> -- <command>` runs a command under a gate and records what it cost in its project (CPU
  time, peak memory, duration); `djinn command list [--project <name>]` shows the costs. Held outside a running
  worker (a person's terminal, a lead, `djinn gate hold <name> --pid <pid>`), a gate takes a worker's slot, is
  measured when its process is known, and goes back once that process ends or after its timeout (an hour by default).
  Next time, the gate waits until the machine has the memory the command peaked at. On Linux with a user systemd,
  each worker runs in a systemd user scope of its own (`djinn-<task code>-<uuid8>`), which stopping, pausing and
  measuring take whole, even a process that left its group; elsewhere `djinn up` says so once, and workers run in their
  process group. `djinn up --worker-cpu 150` caps each worker at 150% of a core, `--worker-memory 4096` at 4 GiB, where
  systemd gives your user the cpu and memory controllers ([T14](plan/8ce817da-cost.md),
  [T02](plan/c30479be-api-and-cli.md)).
- `djinn task get <task-id>` gives what its worker uses (CPU, memory, processes) and its peaks, read every 5 seconds
  from its scope's cgroup on Linux (else from its process group), from its process group on macOS; `djinn machine
  show` lists every running worker's, the busiest first, and the Tasks tab shows them. Not measured on Windows yet
  ([T14](plan/8ce817da-cost.md)). Another worker starts only when the free memory holds the typical peak
  of its provider's workers (1 GiB while none is measured); else its task says what it waits for.
- `go tool task bench-dispatch`: the scheduler's decisions on hand-written dispatch cases, as a table
  ([T07](plan/8e8d3d76-orchestrator.md)).
- `djinn backup [--file <archive>]` copies the data folder, even while Djinn runs; `djinn backup restore <archive>`
  puts it back, Djinn stopped ([`docs/backup.md`](docs/backup.md)).
- `djinn wish set-lead <wish-id> <session-id> --directory <folder>` records a wish's lead session;
  `djinn wish resume <wish-id>` shows the wish and resumes its lead in the window's terminal,
  starting Djinn if needed. A second `djinn up` brings the window to the front.
- `djinn wish route "<request>" --wish-id <wish-id> --ask`: a request that is not about the wish becomes a card, without a
  model: open a new one whose lead starts on it in its own terminal, file it in an existing wish, or keep it here
  ([T26](plan/e1210c5e-request-routing.md)). A request that matches a skill's wish template (`metadata.djinn.wish` in its
  `SKILL.md`) makes that skill's wish, its watcher started and its lead on the skill: `.agents/skills/babysit-pr` for a
  GitHub pull request, `.agents/skills/babysit-mr` for a GitLab merge request
  ([`docs/wish-templates.md`](docs/wish-templates.md)).
- `djinn wish brief <wish-id>`: where the wish stands and how to lead it, computed from the store; every new lead, of
  any agent, starts by running it. `djinn wish describe <wish-id> --text "…"` gives the wish the few lines it opens with
  (also edited under the wish's title). `djinn wish resume <wish-id> --provider antigravity` (or the arrow beside Lead)
  starts a lead of another agent than the recorded one's.
  `djinn task spawn … --fork W1` or `--from-lead` starts a worker from a copy of a conversation; `djinn up
  --warm-workers` keeps a claude loaded per project; `go tool task bench-workers` (paid, refuses without consent)
  compares them ([T14](plan/8ce817da-cost.md)).
- `djinn task send <task-id> "…"` gives a running worker an instruction (or the box under its events, in the window):
  an event of the task, then "received" once the worker says something after it.
- A decision is an answered question or a block of kind decision. `djinn question ask … --icon 🔒` and `djinn block put
  … --icon 🧱` give it its subject's emoji (one emoji); `djinn task spawn … --decision Q43` (a question's code, or a
  decision block's id) says which decision a task comes from. The window lists them in a "Decisions" tab, read only:
  who took each one (you, the lead, a worker) and the tasks it led to; the page and the brief follow.
- An answer starts a question worker, "Q43 → tasks", that turns the decision into tasks (`--decision Q43`); "Enlighten
  me" starts "Q43: enlighten", which revises the question. They read, take no slot (only memory), and tell the lead what they did.
  `question_workers: false` in a project's settings ([`docs/team-settings.md`](docs/team-settings.md)) or `djinn up
  --question-workers=false` turns them off. A worker's `djinn block put`, `question ask` and `revise` name its task from
  `$DJINN_TASK_ID`: the decision log says "By W12".
- An open question is blocking when a task waits for its answer, red; `djinn question ask … --before "before the merge"`
  makes it orange under those words; without them it can wait, grey. `djinn question revise … --before …` changes them,
  `--before ""` lets it wait. Everywhere, blocking comes first, then before X, then can wait.
- With the window, a question asked in an active wish shows as a system notification: a click shows the wish, a
  button answers it. A new inbox item shows as one too, and the side panel counts the new ones on the flight plan. On macOS it needs the `.app` bundle (T19); a headless build and `--browser` show none.
- `go tool task check-window`: open a window a few seconds and check that a stream reaches it
  value by value. PASS or FAIL.
- `go tool task e2e-native`: drive the real window end to end through the Wails MCP server (a test build, window
  "Djinn e2e"). Opens a window a few seconds, so it is not in `test` ([T05](plan/b6a680bf-testing.md)).
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
  [goldmark](https://github.com/yuin/goldmark) (the Markdown of a wish's page),
  [godbus](https://github.com/godbus/dbus) and [go-toast](https://git.sr.ht/~jackmordaunt/go-toast) (the system
  notifications of the window, through Wails, on Linux and on Windows),
  [x/sys](https://github.com/golang/sys) (also the terminal's pseudo-console on Windows), [x/term](https://github.com/golang/term) (tests),
  [x/mod](https://github.com/golang/mod) (release versions), [x/net](https://github.com/golang/net) (the text of a
  tilasm's page, by its HTML tokenizer),
  [go-yaml](https://github.com/yaml/go-yaml) (the front matter of a skill's wish template); as module tools,
  [Task](https://github.com/go-task/task), [buf](https://github.com/bufbuild/buf) and
  [golangci-lint](https://github.com/golangci/golangci-lint) (GPL-3.0: run on the code, in a module of its own, never
  built into Djinn).
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
- Documentation site, vendored in `docs/site`: Cormorant Garamond, DM Sans and IBM Plex Mono from Fontsource.
- Build and tests: [Vite](https://github.com/vitejs/vite) and
  [its React plugin](https://github.com/vitejs/vite-plugin-react),
  [TypeScript](https://github.com/microsoft/TypeScript),
  [Playwright](https://github.com/microsoft/playwright),
  [Prettier](https://github.com/prettier/prettier),
  [ESLint](https://github.com/eslint/eslint) with [typescript-eslint](https://github.com/typescript-eslint/typescript-eslint),
  [ESLint React](https://github.com/Rel1cx/eslint-react),
  [eslint-plugin-react-hooks](https://github.com/facebook/react) and [globals](https://github.com/sindresorhus/globals),
  [DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped).
- Adapted code and assets are credited in [`docs/THIRD_PARTY_NOTICES.md`](docs/THIRD_PARTY_NOTICES.md).

## Licenses: follow them to the letter
Djinn is licensed under Apache-2.0 (`LICENSE`), with its copyright notice in `NOTICE`.

- **Before reusing or adapting third-party code** (a file, a function, a model catalog, an
  algorithm taken from a repository), check its license:
  - MIT, BSD, ISC, Apache-2.0: allowed, with attribution as below.
  - MPL-2.0: allowed only as an unmodified dependency, never copied into our files.
  - GPL, AGPL, LGPL, SSPL, BUSL or no license: do not copy. Read for ideas only, and say so in
    the change description.
- **Cite it in [`docs/THIRD_PARTY_NOTICES.md`](docs/THIRD_PARTY_NOTICES.md)**, following that license's own
  conventions: the project name, the source URL and the commit or version, what was adapted, the
  original copyright line, and the full license text when the license requires it (MIT and BSD
  do). Keep a short pointer in a comment at the top of the adapted file.
- **A new dependency** (Go module or npm package) needs a license from the allowed list. Name it
  in the change description.
- Never remove or alter a copyright notice or a license header from third-party code.

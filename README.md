<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/site/assets/mark-dark.svg">
    <img src="docs/site/assets/mark.svg" width="88" height="88" alt="Djinn">
  </picture>
</p>

# Djinn

_A wisp of smoke to work your will._

> **Alpha.** Djinn is young and moves fast. It runs real work every day on its own repository, but expect rough
> edges, and read [Status](#status) before you trust it with yours.

Djinn turns what you wish into work done by coding agents, on your own machine, with your own accounts. It is one
command, `djinn`: a native window, a local server, and a command line the agents use. You make **wishes**; one
**Djinn**, the lead, plans each of them into tasks; **workers** of several agents (Claude Code, Codex, Antigravity)
carry them out, each in its own Git worktree; Djinn schedules, checks, merges and pushes their work. Everything that
needs you comes as a **question**, with a recommendation and the exact command when a move is yours to make.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/readme/questions-light.png">
  <img alt="A wish in Djinn: what waits for you, and a question with the Djinn's recommendation" src="docs/screenshots/readme/questions-dark.png">
</picture>

## What no djinn can grant

Like the genie of the lamp, Djinn has limits, and they are the project's first rule:

- **It can't kill anybody.** Nothing destructive or irreversible (a deletion, a force push, a deployment) happens
  without your explicit go.
- **It can't make anybody fall in love.** It never bends anyone else's will: no message, approval or merge sent in
  your name that you have not seen.
- **It can't bring anyone back from the dead.** What failed stays failed: no made-up result, every claim comes with
  its evidence.
- **No wishing for more wishes.** It never grants itself more than it was given: no extra budget, no wider access,
  and no secret it stores or reads.
- **Three wishes at a time.** One Djinn holds three active wishes at most, and as many set aside as you like.

**Workers commit, and never push.** Each commits its work in its worktree; Djinn merges each one's work as it ends,
checked, and pushes on a cadence: one CI run per push instead of one per worker.

## Install

One line, no administrator rights. The script picks the build for your system, checks it against the release's
`SHA256SUMS`, and puts `djinn` in your own folders (`~/.local/bin` on macOS and Linux). On Linux without WebKitGTK it
installs a build that opens Djinn in your browser.

```sh
curl -fsSL https://github.com/Empowill/Djinn/releases/latest/download/install.sh | sh    # macOS, Linux
```

```powershell
irm https://github.com/Empowill/Djinn/releases/latest/download/install.ps1 | iex         # Windows
```

Then run `djinn up` (`djinn up --browser` prints a URL instead of opening a window). The latest release is
[v0.2.1](https://github.com/Empowill/Djinn/releases/tag/v0.2.1).

**Agents.** Djinn asks for no API key and stores none: it runs the agents you installed and signed in to, and your
account pays for them. **Connections & preferences** shows which ones it finds and installs or signs them in from the
window; the **Summoning Circle** picks the agent and model for the Djinn and for each kind of worker. By hand:
`npm install -g @anthropic-ai/claude-code`, `npm install -g @openai/codex`, and for Antigravity see
[`docs/providers.md`](docs/providers.md#antigravity).

**macOS builds are not signed yet.** The `curl` install is not affected; a `Djinn.app` downloaded with a browser
must be allowed once in *System Settings › Privacy & Security*, or with `xattr -dr com.apple.quarantine Djinn.app`.

### From source

With Go 1.25+ (and CGO for the window), Node.js 22, and on Linux the GTK and WebKitGTK headers (the full list is in
[`CONTRIBUTING.md`](CONTRIBUTING.md#getting-set-up)):

```sh
git clone https://github.com/Empowill/Djinn.git && cd Djinn
npm ci
go tool task install    # installs djinn in Go's bin folder, with its menu entry on Linux
djinn up
```

## What works today

What is below is merged on the alpha branch and covered by tests (see [release notes](docs/releases/) for what ships
in each tagged build):

- **Wishes and azimas.** A wish is what you ask for, across one or more projects. The Djinn splits it into _azimas_
  (goal-sized parts of the plan, coded `T01`, `T02`…) and tasks in a conflict-free parallel graph; each work task is
  one worker in its own worktree. Azimas are ranked by priority (the scheduler and gates serve the first azimas first),
  can wait as drafts, move between wishes, ask for your validation once their work is done, and show **Merged into
  main** when their branch or pull request is in main. [Guide](docs/user-guide.md#a-wish) ·
  [Azimas](docs/user-guide.md#azimas)
- **One driven Djinn for every wish.** A single lead sees every active wish, so it spots duplicate or conflicting
  work. It starts from `djinn brief`, a summary Djinn writes without a model, with its safe read, Git and `djinn`
  commands pre-authorized and its notes built into the binary (`djinn lead notes`). By default, Djinn drives it without
  a terminal, in a fresh Claude Code or Codex session per batch of events (`djinn lead journal`, `djinn lead sessions`):
  a chip at the top names where it runs and moves it across agents, accounts or models, the side journal retries a
  failed batch, and **the Djinn's eye** follows the batch and holds its replies on **Write to the Djinn** (Cmd/Ctrl+I).
  Its voice follows the window's theme (solar in light mode, demon in dark). [Guide](docs/user-guide.md#the-djinn)
- **Questions and moves.** The Djinn says nothing for you in a terminal: every report, doubt, choice or "done" is a
  question (`Q01`, `Q02`…) in its wish, ordered by urgency (**Blocking**, **Your move**, **To decide**, **Can wait**)
  with options and a recommendation. **Rub the lamp** takes the recommended choice in one click or key; options that
  declare their next worker (`Builds:`, `Iterates:`, `Investigates:`) launch it at once (**Build**, **Iterate**,
  **Investigate**). **Enlighten me** sends an investigator worker (with web search and expanded read access) to dig
  first and revise the question, keeping earlier revisions at the bottom; **Later** and **Sleep** set a question aside
  until a task ends, a pull request merges, or a date passes. A move only you can make (push a tag, open or merge a
  pull request, check on a Mac) puts every command to run in one code block at the top; a check or a CI result is never
  asked of you. [Guide](docs/user-guide.md#questions)
- **Tilasms.** A tilasm (`L01`, `L02`…) is a self-contained, sandboxed HTML page (`djinn://tilasm/<id>`) the Djinn or a
  worker writes to show an analysis, a comparison, a diagram or an interactive mockup—kept and versioned (`v1`, `v2`…)
  in Djinn's data folder, never in your repository. It opens full size over a question or in the **Tilasms** tab; you
  select a passage or click an element to leave inline comments, then **Iterate** or **Launch** sends them in one batch
  for the Djinn to answer and carry across versions. [Guide](docs/user-guide.md#the-tilasms-tab)
- **Investigations, mockups and ifrits.** Beyond regular work tasks, the Djinn can start an **investigation** (a short,
  time- and cost-bounded study) or a **mockup** (multi-round visual exploration), both running in their own worktree
  without ever merging into your branch, preempting regular work and ending in a tilasm and a question. An **ifrit** is
  an interactive worker that stays lit while you are active in the window or terminal (keeping its dev servers up via
  `djinn serve` with clickable port links), goes out after an idle delay to free its slot and ports, relights on
  demand, and merges its work through the project's checks when finished.
  [Guide](docs/user-guide.md#an-ifrit-a-worker-that-stays-lit-while-you-are-there)
- **Watchers, pull requests babysat, templates and the Inbox.** A **watcher** (`--provider watch`) is an agentless
  command task—zero tokens, zero worker slots—that reads its command's output by paragraphs and wakes the Djinn on
  change. Project skills (`SKILL.md`, summonable across projects) can declare **wish templates** for recurring
  requests, **babysit** watchers that follow every pull or merge request a task opens (`gh pr create`,
  `glab mr create`) until it merges, and **Inbox** sources (`djinn inbox plug`) that surface outside requests at the
  top of the flying carpet. [Watchers & templates](docs/user-guide.md#watchers-and-wish-templates) ·
  [Babysitting](docs/user-guide.md#babysitting-pull-requests) · [Inbox](docs/user-guide.md#the-inbox)
- **The flying carpet and the Hourglass.** **The flying carpet** (Ctrl+1 / Cmd+1) shows every active wish at once, the
  inbox, and what waits for you first. **The Hourglass** (Ctrl+2 / Cmd+2) shows **Resources** (side-by-side CPU and
  memory heatmaps over time, and workers grouped by wish), **Gates** (where command time goes), and **Disk** (whole-disk
  usage and cleanup commands ordered by risk). [Flying carpet](docs/user-guide.md#the-flying-carpet) ·
  [Hourglass](docs/user-guide.md#the-hourglass)
- **Your work lands where you choose.** Per wish, one integration branch; per azima, one branch cut from main with
  its own pull request (naming a branch after a remote one fetches it first, and answering yes to Djinn's pull request
  question opens it as a draft). Djinn merges each task's work, runs the project's checks, starts a correction worker
  when they fail, and pushes on a standard cadence or on demand. [Guide](docs/user-guide.md#team-settings-and-integration)
- **Review.** Each task shows its whole diff; a comment on a line reaches its worker or becomes a correction task. A
  **Review** tab lists the merged work to tick off. [Guide](docs/user-guide.md#the-review-tab)
- **The Lamplighter.** A watchdog wakes a worker gone silent and continues it on its own session; each task has a
  _seal_ (2 hours of work, 500 turns per run shown beside its lifetime turns, a spending cap, what it may edit and
  run). Stopping Djinn never loses a worker: the next start resumes it. [Guide](docs/user-guide.md#when-djinn-stops-and-starts-again)
- **Your machine stays yours.** An operating load slider, from **Minimal** to **Overclock** or **Auto**, sets how
  much of the machine the workers take and shows the exact worker count for each notch; heavy commands share it
  through gates.
- **Nothing outlives Djinn.** Killing Djinn stops every worker, watcher and check it started; a survivor is
  stopped at the next start.
- **Logs and a debug dump.** `logs/djinn.log` in the data folder records each call, command, error and panic (with
  its stack), never a prompt or a secret. When Djinn freezes, `djinn debug dump` writes where each part of
  it waits, even when its server no longer answers. [Guide](docs/user-guide.md#when-djinn-is-stuck)
- **Updates on a click.** A banner offers a newer Djinn with its release notes; nothing restarts without you.

### Systems

| System  | Builds                                         | What is verified                                                                                                                               |
| ------- | ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Linux   | amd64, arm64: GTK 3, GTK 4, browser            | Daily use, on amd64 with GTK 3. Every test runs in CI.                                                                                         |
| macOS   | one universal binary, and `Djinn.app`          | Used on a Mac during the port; the native end-to-end test passes there. Go tests run in CI. The release archives are not checked by hand yet. |
| Windows | amd64, arm64                                   | Go, interface and end-to-end tests run in CI, not yet as a required check. Not checked yet on a physical machine.                             |

## Status

Djinn is **alpha**. It builds itself every day, with a Claude Code lead on Linux; much else is covered by tests but
not yet by use. Not verified yet:

- **Codex** is tested against hand-written streams only: Djinn has not yet run a real Codex, as a lead or a worker.
  Antigravity has run real tasks; it reports no sign-in status.
- **macOS by hand** from a release: `Djinn.app` opened from the Finder, its notifications, the title bar. The builds
  are not signed nor notarized.
- **Windows and arm64 Linux on real hardware**, including the Windows window and SmartScreen. On Windows, workers
  cannot be paused and their CPU and memory are not measured yet.
- **GitLab**: a merge request per azima, and one babysat until it merges, end to end.
- **An update from inside the app** from one release to the next.

The data folder is kept across versions, but the command line and the API may still change.

## Beta roadmap

Four workstreams lead from alpha to beta:

1. **Distribution of work across trusted machines** ([`T28`](plan/b4286211-work-spread-over-trusted-machines.md)).
2. **Collaboration in general on wishes, to wish together** ([`T29`](plan/e6e6605e-collaborate-on-a-wish.md)).
3. **Review in general for wishes (QA, code review, decisions…).**
4. **The project's art direction, and simpler reading to make everyday use pleasant.**

## Roadmap to v1

Djinn is tracked here, with no other tool: one box per task, one file per task in
[`plan/`](plan/README.md), with what to do and the questions still open.

**Phase 1 · Native app**

- [x] [T01 · The native window: the same interface as in the browser, where you talk to the lead, by voice too](plan/fe63ed30-native-window.md)
- [x] [T02 · The foundation: the protos, minimal data, a generated command line, in English, clean code](plan/c30479be-api-and-cli.md)
- [x] [T04 · Install and stay up to date: in a few minutes, updates from inside the app](plan/929d6a88-getting-started.md)
- [x] [T05 · Tested end to end: fast tests, and an agent that checks its work up to the real window](plan/b6a680bf-testing.md)
- [x] [T11 · Windows](plan/a586b68b-windows.md)

**Phase 2 · Orchestration and data**, after which Djinn runs on itself

- [x] [T07 · The orchestrator: Djinn decides, starts, resumes, integrates and pushes the work by itself](plan/8e8d3d76-orchestrator.md)
- [x] [T13 · Work across projects: a wish spanning several projects, a project's skills in another](plan/58ae4a59-cross-project-sessions.md)
- [x] [T14 · Fast, economical workers: the right context and model, sized to the machine](plan/8ce817da-cost.md)
- [x] [T18 · Antigravity as a worker provider](plan/a72eb1f8-antigravity-provider.md)
- [x] [T25 · Understand and decide at a glance: what waits for you first, clear questions, tilasms](plan/4a699d0f-review-at-a-glance.md)
- [x] [T26 · Every request finds its wish: routing, wish templates, an inbox](plan/e1210c5e-request-routing.md)
- [x] [T27 · Djinn stays fast, whatever the size of a wish: the window, djinn up and its start](plan/9ac1c08b-djinn-stays-fast.md)

**Phase 3 · Comfort**

- [x] [T19 · Releases: binaries for every target](plan/71f9c331-releases.md)
- [x] [T20 · Your wishes follow you: backups, a wish online, later your other machines](plan/9b71f059-backup.md)

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/readme/tasks-light.png">
  <img alt="The same wish: its tasks, by status, and what they cost" src="docs/screenshots/readme/tasks-dark.png">
</picture>

_Screenshots of a demonstration wish, with fictional data. `go tool task screenshots` takes them again._

## Documentation

[User guide](docs/user-guide.md) · [Providers](docs/providers.md) · [Agent protocol](docs/agent-protocol.md) ·
[Team settings](docs/team-settings.md) · [Wish templates](docs/wish-templates.md) · [Backups](docs/backup.md) ·
[Release notes](docs/releases/)

Every concept and command in one site: **Grimoire** in the window's side panel, served at `/docs/` while
`djinn up` runs, or `go tool task docs`, then `bin/docs/index.html`.

## Contributing

Everything you need to work on Djinn, person or agent, is in [`CONTRIBUTING.md`](CONTRIBUTING.md): the shape of the
project (the lamp and the smoke), its technical directions, how to set up, and its license rules. Coding agents read
[`AGENTS.md`](AGENTS.md) too. `go tool task test` runs every test, and no test calls a paid model.

## License

Djinn is free and open-source software under the [Apache License, Version 2.0](LICENSE). Third-party dependencies and
their licenses are listed in [`docs/THIRD_PARTY_NOTICES.md`](docs/THIRD_PARTY_NOTICES.md), shipped with every release.

## Credits

Thanks to Clément Fauvelle ([@clemzer0o](https://github.com/clemzer0o)) for the macOS and Codex port and for the
design of the window. The engine and some adapters are studied from [T3 Code](https://github.com/pingdotgg/t3code).
The avatars use [Thinking Orbs](https://libraries.dev/orbs). Djinn's diagrams and illustrations are original.

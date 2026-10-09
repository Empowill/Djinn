# Djinn

_A wisp of smoke to work your will._

**Wish for anything, except what no djinn can grant.** Like the genie of the lamp, Djinn has its
limits, and they are the project's first rule:

- **It can't kill anybody.** Nothing destructive or irreversible (a deletion, a force push, a
  deployment) happens without your explicit go.
- **It can't make anybody fall in love.** It never bends anyone else's will: no
  message, approval or merge sent in your name that you have not seen.
- **It can't bring anyone back from the dead.** What failed stays failed: no made-up result, every
  claim comes with its evidence.
- **No wishing for more wishes.** It never grants itself more than it was given: no extra budget,
  no wider access, and no secret it stores or reads.
- **Three wishes at a time.** Never more: one Djinn holds three active wishes at most, and as many set aside as
  you like.

**Workers never commit.** They edit their worktree; the lead reviews each diff, commits in batches and pushes
once. Fewer, clearer commits, and one CI run instead of one per worker: the Git runners breathe.

> **ALPHA — NOT USABLE AS IS.** Djinn is an experimental project under development. It is not ready to be used, neither for real work nor in production. The features described below show the project's goal and its state of development; they are no guarantee that it works.

**Give an intent. Stay in control of your agents' work.**

Djinn is a local desktop app to work with **Codex** and **Claude Code**: a readable timeline, agents followed live, explicit decisions, and results you can open, annotate and validate.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/readme/questions-light.png">
  <img alt="A wish in Djinn: what waits for you, and a question with the lead's recommendation" src="docs/screenshots/readme/questions-dark.png">
</picture>

## Install

**Coming with the first release: no release exists yet, so these lines do not work today.** One line, no sudo:
the binary for your system, checked against its SHA-256 sum, in your own folders. On Linux without WebKitGTK, it
takes the browser build (the same app, in your browser); with no binary that fits, `go install`.

```sh
curl -fsSL https://github.com/Empowill/Djinn/releases/latest/download/install.sh | sh    # macOS, Linux
```

```powershell
irm https://github.com/Empowill/Djinn/releases/latest/download/install.ps1 | iex         # Windows
```

**Antigravity, optional.** Workers run Claude Code or Codex. Google's Antigravity (`agy`) can run them too: install
it, sign in with your Google Cloud project (`gcloud auth application-default login --project <project>`, then
`AGY_ADC_AUTH=true` where `djinn up` runs) or with agy's own sign-in, then `djinn task spawn --provider antigravity`.
Djinn runs the official `agy` as installed and never reads its credentials; the account you sign in with pays.
Steps and rules: [`docs/providers.md`](docs/providers.md#antigravity).

## Roadmap to v1

Djinn is tracked here, with no other tool: one box per task, one file per task in
[`plan/`](plan/README.md), with what to do and the questions still open.

**Phase 1 · Native app**

- [ ] [T01 · The native window: Wails, the embedded interface, dev and browser modes](plan/fe63ed30-native-window.md) (in progress)
- [x] [T02 · Protos, API and command line: everything generated, a CLI by convention](plan/c30479be-api-and-cli.md)
- [x] [T03 · Keep the interface working: the `window.djinn` shim](plan/aef418cb-interface-shim.md)
- [ ] [T04 · Getting started: install in one line, or ask your agent](plan/929d6a88-getting-started.md) (in progress)
- [x] [T05 · Testing: unit tests in seconds, `task e2e` an agent can run](plan/b6a680bf-testing.md)
- [ ] [T06 · End-to-end tests on the native window](plan/b81b5d99-native-e2e.md) (in progress)
- [x] [T10 · English everywhere](plan/4527d734-english-everywhere.md)
- [ ] [T11 · Windows](plan/a586b68b-windows.md)
- [ ] [T12 · Updates from inside the app](plan/1aa20487-auto-update.md) (in progress)

**Phase 2 · Orchestration and data**, after which Djinn runs on itself

- [ ] [T07 · The orchestrator: workers, worktrees, scheduling, the machine](plan/8e8d3d76-orchestrator.md) (in progress)
- [x] [T17 · Know the machine, spend it wisely](plan/3da7b334-machine-discovery.md)
- [ ] [T18 · Antigravity as a worker provider](plan/a72eb1f8-antigravity-provider.md) (in progress)
- [x] [T21 · The lead's terminal, inside the app, by voice](plan/17ed4dcd-lead-terminal.md)
- [ ] [T22 · Workers that start fast, with the right context, and are measured](plan/1689571a-fast-workers.md) (in progress)
- [x] [T08 · Data: what we store, and why](plan/716b9f97-data.md)
- [x] [T13 · Sessions across projects](plan/58ae4a59-cross-project-sessions.md)
- [ ] [T26 · Every request finds its wish: routing](plan/e1210c5e-request-routing.md) (in progress)
- [ ] [T27 · Wish templates, drawn from skills](plan/bac5e018-wish-templates.md) (in progress)
- [ ] [T29 · Tilasms: what explains a wish, kept, linked, opened anywhere](plan/eee91edb-tilasms.md)
- [ ] [T30 · Djinn integrates finished work by itself](plan/43303f46-integration.md)

**Phase 3 · Comfort**

- [x] [T14 · Spend big models only where they matter](plan/8ce817da-cost.md)
- [ ] [T16 · Dispatch: plain Go code or a local model?](plan/263f074f-dispatch-bench.md) (in progress)
- [ ] [T09 · Quality of life and clean-up](plan/3792046b-quality-of-life.md) (in progress)
- [ ] [T19 · Releases: binaries for every target](plan/71f9c331-releases.md) (in progress)
- [x] [T20 · Backups, on a server of your choice](plan/9b71f059-backup.md)
- [ ] [T23 · Summon a skill from another project](plan/a5c284be-summon-skills.md) (in progress)
- [ ] [T24 · A wish online: sync now, collaborate later](plan/7dc376e9-wish-online.md) (in progress)
- [x] [T25 · Review and decide at a glance](plan/4a699d0f-review-at-a-glance.md)
- [ ] [T28 · An inbox: what comes from outside becomes a proposed wish](plan/d6fb2417-inbox.md) (in progress)

**After v1**

- [ ] [T15 · Work spread over trusted machines](plan/9c8f55df-distributed-work.md)

## From brief to result

- **One wish, one lead.** Say what you wish. Its lead, Claude Code or Codex, splits it into tasks, each one a worker in
  its own worktree. Three wishes are active at most; the flight plan shows them together.
- **A visible team.** Each task shows its worker, its status, its live events and what it cost. Send it an instruction
  while it works, or stop it. Talk to the lead in a terminal inside the window.
- **You decide.** A question comes with its options and the lead's recommendation: **Rub the lamp** takes it,
  **Enlighten me** sends the lead to dig first. A system notification answers it from anywhere.
- **The lead's notes stay in the wish.** Markdown and Mermaid diagrams: mark what you read, approve what may go on
  as it is. One HTML page shares the wish; `djinn wish export` hands it over whole, in one file.
- **Nothing is lost.** Stopping Djinn interrupts the workers, never loses them: the next start resumes each one in
  its own worktree and session. A worker stopped by its usage limit waits for the reset, then goes on.
- **Every request finds its wish.** Something unrelated comes up: the lead offers to open a new wish, to file it in
  an existing one, or to keep it where it is. A skill's [wish template](docs/wish-templates.md) makes a wish that already knows how to work, with
  a watcher that wakes its lead on each change.
- **Your language.** The interface follows the system language (English or French); change it in **Connections &
  preferences**.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/readme/tasks-light.png">
  <img alt="The same wish: its tasks, what they cost, and the lead's notes" src="docs/screenshots/readme/tasks-dark.png">
</picture>

_Screenshots of a demonstration wish, with fictional data. `go tool task screenshots` takes them again._

## Local development

To build and test Djinn, see [`CONTRIBUTING.md`](CONTRIBUTING.md): `go tool task build`, then `bin/djinn up`;
`go tool task test` runs every test, with no paid model. The window reads its wishes, tasks and questions from the
djinn it runs in: the store stays on your machine, in Djinn's data folder.

[User guide](docs/user-guide.md) · [Agent protocol](docs/agent-protocol.md) · [Adaptable wishes](docs/adaptable-wishes.md)

The concepts and the API, in one site: **Documentation** in the window's settings, or `go tool task docs`, then
`bin/docs/index.html` ([`docs/site`](docs/site), the API from [`docs/openapi.json`](docs/openapi.json)).

## Credits

The engine and some adapters are studied from [T3 Code](https://github.com/pingdotgg/t3code). The avatars use [Thinking Orbs](https://libraries.dev/orbs). Djinn's diagrams and illustrations are original. See [the notices](docs/THIRD_PARTY_NOTICES.md) and [the license](LICENSE).

## Contributing

Everything you need to work on Djinn, person or agent, is in [`CONTRIBUTING.md`](CONTRIBUTING.md):
the shape of the project (the lamp and the smoke), its technical directions, how to set up, and
the projects Djinn stands on.

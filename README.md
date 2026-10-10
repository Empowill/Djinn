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

**Workers never commit.** They edit their worktree; Djinn commits each one's work as it ends, tested, and pushes on
a cadence: at an azima's end, or after three tasks and an hour. One CI run per push instead of one per worker: the Git
runners breathe.

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

- [ ] [T01 · The native window: the same interface as in the browser, where you talk to the lead, by voice too](plan/fe63ed30-native-window.md) (in progress)
- [x] [T02 · The foundation: the protos, minimal data, a generated command line, in English, clean code](plan/c30479be-api-and-cli.md)
- [ ] [T04 · Install and stay up to date: in a few minutes, updates from inside the app](plan/929d6a88-getting-started.md) (in progress)
- [ ] [T05 · Tested end to end: fast tests, and an agent that checks its work up to the real window](plan/b6a680bf-testing.md) (in progress)
- [ ] [T11 · Windows](plan/a586b68b-windows.md) (in progress)

**Phase 2 · Orchestration and data**, after which Djinn runs on itself

- [ ] [T07 · The orchestrator: Djinn decides, starts, resumes, integrates and pushes the work by itself](plan/8e8d3d76-orchestrator.md) (in progress)
- [ ] [T13 · Work across projects: a wish spanning several projects, a project's skills in another](plan/58ae4a59-cross-project-sessions.md) (in progress)
- [ ] [T14 · Fast, economical workers: the right context and model, sized to the machine](plan/8ce817da-cost.md) (in progress)
- [ ] [T18 · Antigravity as a worker provider](plan/a72eb1f8-antigravity-provider.md) (in progress)
- [ ] [T25 · Understand and decide at a glance: what waits for you first, clear questions, tilasms](plan/4a699d0f-review-at-a-glance.md) (in progress)
- [ ] [T26 · Every request finds its wish: routing, wish templates, an inbox](plan/e1210c5e-request-routing.md) (in progress)
- [ ] [T27 · Djinn stays fast, whatever the size of a wish: the window, djinn up and its start](plan/9ac1c08b-djinn-stays-fast.md) (in progress)

**Phase 3 · Comfort**

- [ ] [T19 · Releases: binaries for every target](plan/71f9c331-releases.md) (in progress)
- [ ] [T20 · Your wishes follow you: backups, a wish online, later your other machines](plan/9b71f059-backup.md) (in progress)

## From brief to result

- **One wish, one lead.** Say what you wish. Its lead, Claude Code or Codex, splits it into tasks, each one a worker in
  its own worktree. Three wishes are active at most; the flight plan shows them together.
- **A visible team.** Each task shows its worker, its status, its live events and what it cost. Send it an instruction
  while it works, or stop it. Talk to the lead in a terminal inside the window.
- **You decide.** A question comes with its options and the lead's recommendation: **Rub the lamp** takes it, and a
  small worker turns your decision into tasks; **Enlighten me** sends one to dig first, then revise the question. The
  lead hears what each did. A system notification answers it from anywhere.
- **What needs you is a question; the rest stays in the wish.** What the lead and the workers write down for one
  another, Markdown and Mermaid diagrams, waits in a tab of its own. One HTML page shares the wish; `djinn wish
  export` hands it over whole, in one file.
- **Nothing is lost.** Stopping Djinn interrupts the workers, never loses them: the next start resumes each one in
  its own worktree and session. A worker stopped by its usage limit waits for the reset, then goes on.
- **Every request finds its wish.** Something unrelated comes up: the lead offers to open a new wish, to file it in
  an existing one, or to keep it where it is. A skill's [wish template](docs/wish-templates.md) makes a wish that already knows how to work, with
  a watcher that wakes its lead on each change.
- **Your language.** The interface follows the system language (English or French); change it in **Connections &
  preferences**.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/readme/tasks-light.png">
  <img alt="The same wish: its tasks, by status, and what they cost" src="docs/screenshots/readme/tasks-dark.png">
</picture>

_Screenshots of a demonstration wish, with fictional data. `go tool task screenshots` takes them again._

## Local development

To build and test Djinn, see [`CONTRIBUTING.md`](CONTRIBUTING.md): `go tool task build`, then `bin/djinn up`;
`go tool task test` runs every test, with no paid model. The window reads its wishes, tasks and questions from the
djinn it runs in: the store stays on your machine, in Djinn's data folder.

[User guide](docs/user-guide.md) · [Agent protocol](docs/agent-protocol.md) · [Adaptable wishes](docs/adaptable-wishes.md)

The concepts and every command of the command line, in one site: **Documentation** in the window's settings, or
`go tool task docs`, then `bin/docs/index.html` ([`docs/site`](docs/site), its Command line tab filled in from
djinn's own commands).

## Credits

The engine and some adapters are studied from [T3 Code](https://github.com/pingdotgg/t3code). The avatars use [Thinking Orbs](https://libraries.dev/orbs). Djinn's diagrams and illustrations are original. See [the notices](docs/THIRD_PARTY_NOTICES.md) and [the license](LICENSE).

## Contributing

Everything you need to work on Djinn, person or agent, is in [`CONTRIBUTING.md`](CONTRIBUTING.md):
the shape of the project (the lamp and the smoke), its technical directions, how to set up, and
the projects Djinn stands on.

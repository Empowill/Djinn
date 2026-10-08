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
- **Three wishes at a time.** Never more: one Djinn holds three active wishes at most.

**Workers never commit.** They edit their worktree; the lead reviews each diff, commits in batches and pushes
once. Fewer, clearer commits, and one CI run instead of one per worker: the Git runners breathe.

> **ALPHA — NOT USABLE AS IS.** Djinn is an experimental project under development. It is not ready to be used, neither for real work nor in production. The features described below show the project's goal and its state of development; they are no guarantee that it works.

**Give an intent. Stay in control of your agents' work.**

Djinn is a local desktop app to work with **Codex** and **Claude Code**: a readable timeline, agents followed live, explicit decisions, and results you can open, annotate and validate.

![A wish in Djinn: steps, team and decisions](docs/screenshots/readme/mission.png)

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

## Roadmap to v1

Djinn is tracked here, with no other tool: one box per task, one file per task in
[`plan/`](plan/README.md), with what to do and the questions still open.

**Phase 1 · Native app**

- [ ] [T01 · The native window: Wails, the embedded interface, dev and browser modes](plan/fe63ed30-native-window.md) (in progress)
- [x] [T02 · Protos, API and command line: everything generated, a CLI by convention](plan/c30479be-api-and-cli.md)
- [ ] [T03 · Keep the interface working: the `window.djinn` shim](plan/aef418cb-interface-shim.md) (in progress)
- [ ] [T04 · Getting started: install in one line, or ask your agent](plan/929d6a88-getting-started.md)
- [x] [T05 · Testing: unit tests in seconds, `task e2e` an agent can run](plan/b6a680bf-testing.md)
- [ ] [T06 · End-to-end tests on the native window](plan/b81b5d99-native-e2e.md) (in progress)
- [ ] [T10 · English everywhere](plan/4527d734-english-everywhere.md) (in progress)
- [ ] [T11 · Windows](plan/a586b68b-windows.md)
- [ ] [T12 · Updates from inside the app](plan/1aa20487-auto-update.md) (in progress)

**Phase 2 · Orchestration and data**, after which Djinn runs on itself

- [ ] [T07 · The orchestrator: workers, worktrees, scheduling, the machine](plan/8e8d3d76-orchestrator.md) (in progress)
- [ ] [T17 · Know the machine, spend it wisely](plan/3da7b334-machine-discovery.md) (in progress)
- [ ] [T18 · Antigravity as a worker provider](plan/a72eb1f8-antigravity-provider.md) (in progress)
- [ ] [T21 · The lead's terminal, inside the app, by voice](plan/17ed4dcd-lead-terminal.md) (in progress)
- [ ] [T22 · Workers that start fast, with the right context, and are measured](plan/1689571a-fast-workers.md) (in progress)
- [x] [T08 · Data: what we store, and why](plan/716b9f97-data.md)
- [ ] [T13 · Sessions across projects](plan/58ae4a59-cross-project-sessions.md) (in progress)

**Phase 3 · Comfort**

- [x] [T14 · Spend big models only where they matter](plan/8ce817da-cost.md)
- [ ] [T16 · Dispatch: plain Go code or a local model?](plan/263f074f-dispatch-bench.md)
- [ ] [T09 · Quality of life and clean-up](plan/3792046b-quality-of-life.md) (in progress)
- [ ] [T19 · Releases: binaries for every target](plan/71f9c331-releases.md)
- [x] [T20 · Backups, on a server of your choice](plan/9b71f059-backup.md)
- [ ] [T23 · Summon a skill from another project](plan/a5c284be-summon-skills.md) (in progress)
- [ ] [T24 · A wish online: sync now, collaborate later](plan/7dc376e9-wish-online.md) (in progress)

**After v1**

- [ ] [T15 · Work spread over trusted machines](plan/9c8f55df-distributed-work.md)

## From brief to result

- **One wish, one journey.** Exploration, thinking, specification, prototype, implementation, review and delivery, with a free or a prepared workflow.
- **A visible team.** Follow the lead and its workers, their scopes, dependencies, conversations and real activity.
- **Usable results.** Markdown documents, diagrams, wireframes and interactive visualizations stay in the wish.
- **You decide.** Answer the questions, give guidance, annotate the artifacts and validate the results.
- **Resume without starting over.** The provider tag unfolds: choose Codex or Claude Code, then **Change and resume** after a stop or an exhausted quota. Files, decisions, artifacts and history are kept.
- **A notification for each successful step.** Click it to find the result. Projects and wishes list the most recent first.
- **Your language.** The interface follows the system language (English or French); change it in **Connections & preferences**.

![Djinn's interactive artifacts](docs/screenshots/readme/supports.png)

_Screenshots of the built-in demonstration, with fictional data._

## Local development

To build and test Djinn, see [`CONTRIBUTING.md`](CONTRIBUTING.md): `go tool task build`, then `bin/djinn up`;
`go tool task test` runs every test, with no paid model. The window reads its wishes, tasks and questions from the
djinn it runs in: the store stays on your machine, in Djinn's data folder.

[User guide](docs/user-guide.md) · [Agent protocol](docs/agent-protocol.md) · [Adaptable wishes](docs/adaptable-wishes.md)

## Credits

The engine and some adapters are studied from [T3 Code](https://github.com/pingdotgg/t3code). The avatars use [Thinking Orbs](https://libraries.dev/orbs). Djinn's diagrams and illustrations are original. See [the notices](THIRD_PARTY_NOTICES.md) and [the license](LICENSE).

## Contributing

Everything you need to work on Djinn, person or agent, is in [`CONTRIBUTING.md`](CONTRIBUTING.md):
the shape of the project (the lamp and the smoke), its technical directions, how to set up, and
the projects Djinn stands on.

# Reviewing Djinn

Djinn is one Go command: a native window (Wails v3) embedding a React interface, a local server, and a command line
that coding agents use to drive a wish. Most pull requests are written by Djinn's own workers, opened as drafts, and
marked ready by a maintainer.

## Where the rules live

Read them there; this file only says what to weigh. `files.json` hands them to you.

- [`AGENTS.md`](../AGENTS.md): the rules for any agent working on the code, the interface rules.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): the lamp and the smoke, the technical directions, how tests are written,
  how we work, dependencies and licenses.
- [`docs/pitfalls.md`](../docs/pitfalls.md): the code's known traps, by area.
- [`docs/cli-convention.md`](../docs/cli-convention.md), [`docs/providers.md`](../docs/providers.md),
  [`docs/transport.md`](../docs/transport.md), [`docs/team-settings.md`](../docs/team-settings.md): the conventions of
  their area.
- [`.github/pull_request_template.md`](../.github/pull_request_template.md): what a description says. Its "What to
  read first" names the generated and moved parts.

## What matters most

The lamp is hard: wishes, tasks and their states, questions, decisions, gates, the machine, the journal and the limits
in [`README.md`](../README.md). Weigh first:

- **Nothing is lost.** Every write is a transaction with its journal entry. A stop interrupts workers and the next
  start resumes them. Look for a write outside a transaction, a state that a restart or a crash would drop, a status
  set before or after the event it depends on.
- **Concurrency.** The harness, the dispatch and the gates run many goroutines and processes at once: a lock not
  held, a channel that can block forever, a goroutine that outlives its test or its task.
- **Processes.** What a worker starts dies with it: Djinn stops its group and its leftovers. Look for a process that
  outlives its command, a stop by name instead of by PID, a git that changes a repository outside `gitx.Uncut`, and
  anything that could reach the Djinn the person is using (`docs/pitfalls.md`, Processes).
- **Every machine.** macOS and Linux first, Windows works: paths built with `filepath`, no Unix-only tool in a task.
- **Limits.** The limits of the README are enforced by code; no option turns them off.

## Documentation in the same change

A change to what Djinn does (a feature, a concept, a screen, a command, a behaviour) updates its chapter or card in
`docs/site/index.html` in the same pull request. A command's documentation is its proto comment or its `cli.Builtins`
entry. Say so when it is missing.

## What not to comment on

- Formatting and lint: `go tool task lint` (buf lint, golangci-lint with gofmt and vet, tsc, ESLint, Prettier) runs
  before every merge.
- Generated code under `gen/**`: read the proto under `api/` that it comes from.
- Vendored code (`src/vendor/**`), lockfiles, recorded test streams (`**/testdata/**`) and the translations: the tests
  check the keys.
- The prose of `plan/` and `lead/notes/`: smoke, written by the lead for the lead. Comment only on a fact that is
  false.
- Wording that is short or plain on purpose: the project writes plain English, short sentences, few adjectives.

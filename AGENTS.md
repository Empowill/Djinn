# AGENTS.md

Instructions for any coding agent working on Djinn. `CLAUDE.md` only imports this file: write
shared rules here.

## What Djinn is
One Go command, `djinn`: a native window (Wails v3) embedding the React interface, a local
server, and a command line that agents use to drive a wish.

## Read [`CONTRIBUTING.md`](CONTRIBUTING.md) first
It is the one home of the project's shape (the lamp and the smoke), its technical directions,
its commands and its license rules. They bind agents as much as people; this file does not
repeat them.

## For agents
- **Run the tasks, not the tools.** `go tool task test`, `go tool task gen`, `go tool task e2e`,
  `go tool task build`: they set the tags and the order.
- **Never call a paid model in a test.** Record a stream instead (`docs/providers.md`).
- **A license check comes before any reuse** of third-party code: see the license section of
  `CONTRIBUTING.md`, and name every new dependency in your change description.
- **What you add to a wish's plan is smoke** unless the code computes on it: write a block, do
  not add a field.

# Plan to v1

**One file per task**, named after the last 8 characters of its UUIDv7 (the random part) and a short slug. The
front matter holds the full id, a short code (`T01`) to say it out loud, the phase and the
status. Each file says what to do, when it is done, and the questions still open. Each task is an azima of the wish
that builds Djinn: `djinn plan sync <wish>` reads these files into it, and writes back `after:`, what the azima
depends on, from Djinn's store. Change the graph with `djinn task depend`, never by hand here.

**Status**: `open` when no "Done when" box is checked, `in-progress` when some are, `done` when all are. A box is
checked only with its proof in parentheses (a named test, a command's output); a box that needs a person, a machine
or a real model stays unchecked and says so (`needs: …`). The README checks a task when it is `done`, and marks it
"(in progress)" when it is `in-progress`. `djinn plan sync` reads the "Done when" boxes (nested ones too; a box struck
through with `~~…~~` no longer counts): once the azima's work is finished and every unchecked box says
`(needs: …)`, it awaits its proof; a box without needs is work left. A file whose boxes are all checked closes its
azima whatever its status line says, and the sync reports it: set its status to `done`.

**One goal per azima**: a sentence a user reads as a feature, under the title. Azimas merged into one keep their
content as sections of its file ("## From T03 · …"), each with its own "Done when"; the sync reads every one of them.

The checklist in the main [README](../README.md#roadmap-to-v1) tracks progress; there is no
other tracker and no cloud service. A new task is a new file here, with a fresh UUIDv7.

| Code | Phase | Task | File |
| ---- | ----- | ---- | ---- |
| T01 | 1 | The native window | [fe63ed30-native-window.md](fe63ed30-native-window.md) |
| T02 | 1 | The foundation | [c30479be-api-and-cli.md](c30479be-api-and-cli.md) |
| T04 | 1 | Install and stay up to date | [929d6a88-getting-started.md](929d6a88-getting-started.md) |
| T05 | 1 | Tested end to end | [b6a680bf-testing.md](b6a680bf-testing.md) |
| T11 | 1 | Windows | [a586b68b-windows.md](a586b68b-windows.md) |
| T07 | 2 | The orchestrator | [8e8d3d76-orchestrator.md](8e8d3d76-orchestrator.md) |
| T13 | 2 | Work across projects | [58ae4a59-cross-project-sessions.md](58ae4a59-cross-project-sessions.md) |
| T14 | 2 | Fast, economical workers | [8ce817da-cost.md](8ce817da-cost.md) |
| T18 | 2 | Antigravity as a worker provider | [a72eb1f8-antigravity-provider.md](a72eb1f8-antigravity-provider.md) |
| T25 | 2 | Understand and decide at a glance | [4a699d0f-review-at-a-glance.md](4a699d0f-review-at-a-glance.md) |
| T26 | 2 | Every request finds its wish | [e1210c5e-request-routing.md](e1210c5e-request-routing.md) |
| T27 | 2 | Djinn stays fast, whatever the size of a wish: the window, djinn up and its start | [9ac1c08b-djinn-stays-fast.md](9ac1c08b-djinn-stays-fast.md) |
| T19 | 3 | Releases: binaries for every target | [71f9c331-releases.md](71f9c331-releases.md) |
| T20 | 3 | Your wishes follow you | [9b71f059-backup.md](9b71f059-backup.md) |

## Drafts

| Code | Phase | Draft | File |
| ---- | ----- | ----- | ---- |
| T28 | 3 | Work spread over trusted machines | [b4286211-work-spread-over-trusted-machines.md](b4286211-work-spread-over-trusted-machines.md) |
| T29 | 3 | Collaborate on a wish | [e6e6605e-collaborate-on-a-wish.md](e6e6605e-collaborate-on-a-wish.md) |
| T30 | 2 | Dispatch: local model research | [83566b82-dispatch-local-model.md](83566b82-dispatch-local-model.md) |
| T31 | 3 | Usage analytics with DuckDB | [21e56469-duckdb-analytics.md](21e56469-duckdb-analytics.md) |
| T32 | 3 | Desktop integration ideas | [3be2c459-desktop-integration-ideas.md](3be2c459-desktop-integration-ideas.md) |

Phases: 1 · the native app, 2 · orchestration and data (after which Djinn runs on itself),
3 · comfort and clean-up, delegable.

Principles: the strict minimum, well thought out and adaptable. Protos are the source of
truth. Djinn never stores or reads a secret.


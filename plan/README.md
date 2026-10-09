# Plan to v1

**One file per task**, named after the last 8 characters of its UUIDv7 (the random part) and a short slug. The
front matter holds the full id, a short code (`T01`) to say it out loud, the phase and the
status. Each file says what to do, when it is done, and the questions still open.

**Status**: `open` when no "Done when" box is checked, `in-progress` when some are, `done` when all are. A box is
checked only with its proof in parentheses (a named test, a command's output); a box that needs a person, a machine
or a real model stays unchecked and says so (`needs: …`). The README checks a task when it is `done`, and marks it
"(in progress)" when it is `in-progress`.

The checklist in the main [README](../README.md#roadmap-to-v1) tracks progress; there is no
other tracker and no cloud service. A new task is a new file here, with a fresh UUIDv7.

| Code | Phase | Task | File |
| ---- | ----- | ---- | ---- |
| T01 | 1 | The native window | [fe63ed30-native-window.md](fe63ed30-native-window.md) |
| T02 | 1 | Protos, API and command line | [c30479be-api-and-cli.md](c30479be-api-and-cli.md) |
| T03 | 1 | Keep the interface working: the `window.djinn` shim | [aef418cb-interface-shim.md](aef418cb-interface-shim.md) |
| T04 | 1 | Getting started | [929d6a88-getting-started.md](929d6a88-getting-started.md) |
| T05 | 1 | Testing | [b6a680bf-testing.md](b6a680bf-testing.md) |
| T06 | 1 | End-to-end tests on the native window | [b81b5d99-native-e2e.md](b81b5d99-native-e2e.md) |
| T10 | 1 | English everywhere | [4527d734-english-everywhere.md](4527d734-english-everywhere.md) |
| T11 | 1 | Windows | [a586b68b-windows.md](a586b68b-windows.md) |
| T12 | 1 | Updates from inside the app | [1aa20487-auto-update.md](1aa20487-auto-update.md) |
| T07 | 2 | The orchestrator | [8e8d3d76-orchestrator.md](8e8d3d76-orchestrator.md) |
| T13 | 2 | Sessions across projects | [58ae4a59-cross-project-sessions.md](58ae4a59-cross-project-sessions.md) |
| T17 | 2 | Know the machine, spend it wisely | [3da7b334-machine-discovery.md](3da7b334-machine-discovery.md) |
| T18 | 2 | Antigravity as a worker provider | [a72eb1f8-antigravity-provider.md](a72eb1f8-antigravity-provider.md) |
| T08 | 2 | Data | [716b9f97-data.md](716b9f97-data.md) |
| T21 | 2 | The lead's terminal, inside the app, by voice | [17ed4dcd-lead-terminal.md](17ed4dcd-lead-terminal.md) |
| T22 | 2 | Workers that start fast, with the right context, and are measured | [1689571a-fast-workers.md](1689571a-fast-workers.md) |
| T25 | 2 | Review and decide at a glance | [4a699d0f-review-at-a-glance.md](4a699d0f-review-at-a-glance.md) |
| T26 | 2 | Every request finds its wish: routing | [e1210c5e-request-routing.md](e1210c5e-request-routing.md) |
| T27 | 2 | Wish templates, drawn from skills | [bac5e018-wish-templates.md](bac5e018-wish-templates.md) |
| T14 | 3 | Spend big models only where they matter | [8ce817da-cost.md](8ce817da-cost.md) |
| T16 | 3 | Dispatch: plain Go code or a local model? | [263f074f-dispatch-bench.md](263f074f-dispatch-bench.md) |
| T09 | 3 | Quality of life and clean-up | [3792046b-quality-of-life.md](3792046b-quality-of-life.md) |
| T19 | 3 | Releases: binaries for every target | [71f9c331-releases.md](71f9c331-releases.md) |
| T20 | 3 | Backups, on a server of your choice | [9b71f059-backup.md](9b71f059-backup.md) |
| T23 | 3 | Summon a skill from another project | [a5c284be-summon-skills.md](a5c284be-summon-skills.md) |
| T24 | 3 | A wish online: sync now, collaborate later | [7dc376e9-wish-online.md](7dc376e9-wish-online.md) |
| T28 | 3 | An inbox: what comes from outside becomes a proposed wish | [d6fb2417-inbox.md](d6fb2417-inbox.md) |

**After v1**

| Code | Task | File |
| ---- | ---- | ---- |
| T15 | Work spread over trusted machines | [9c8f55df-distributed-work.md](9c8f55df-distributed-work.md) |

Phases: 1 · the native app, 2 · orchestration and data (after which Djinn runs on itself),
3 · comfort and clean-up, delegable.

Principles: the strict minimum, well thought out and adaptable. Protos are the source of
truth. Djinn never stores or reads a secret.

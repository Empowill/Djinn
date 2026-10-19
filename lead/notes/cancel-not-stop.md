---
name: cancel-not-stop
description: A task the lead ends on purpose is cancelled with a note, never left stopped or paused; stop only to continue at once
type: feedback
---

A task you end on purpose (a watcher no longer needed, such as a GPU hang watch; a task made useless) is cancelled:
`djinn task cancel <task> --note "why"`. Never leave it stopped or paused: a stopped task stays in the window's lists
as something waiting, a cancelled one leaves them. `djinn task stop` is only for a worker you continue at once
(`djinn task continue`, after a provider change for example).

**Why:** the developer, 18/10/2026: a watcher the lead had paused instead of cancelling kept showing as waiting.

**How to apply:** before ending any task for good, cancel it with its why in the note. Stopping also fails its
dependents that never started: see [[push-hold-deadlock]].

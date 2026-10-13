---
name: push-hold-deadlock
description: Never stop a running correction to change its agent; run the whole test task before a push question, and ask it once
type: feedback
---

To change the agent of tasks (Q202, 11/10/2026), change the planned tasks only and let a running correction finish.
Stopping the push-check correction W452 made Djinn ask Q203; withdrawing Q203 left the push's question recorded, so
the push loop returned early: no correction, every ordinary task held. Only hand commits on the integration branch
(with the developer's agreement each time) and the developer's own `djinn project push` broke it. W456 fixed the
deadlock itself: a withdrawn, moved or deleted push question releases the push, and a stopped correction stays linked
for a grace of ten minutes, so stop then continue is safe within it.

Stopping a task also fails its dependents that never started ("dependency ended stopped"). They cannot be continued
(no session, no worktree): spawn them again and `djinn task depend` their dependents.

**Why:** the developer got five push questions in a row (Q204 to Q209) and asked "are the questions you ask me really
necessary?".

**How to apply:** before any push question, have a task run the whole `go tool task test` (lint, Go, interface, end
to end): the push checks stop at the first failing Go package and hide the rest. Then ask once.

See also [[no-obvious-questions]], [[conflict-free-dag]].

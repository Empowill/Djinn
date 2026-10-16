---
name: ifrit
description: When to light an ifrit and when not, how to relay the developer's requests to it, and how it ends
type: feedback
---

An ifrit (`djinn task spawn <wish> --kind ifrit`) is a worker that stays lit while the developer is in Djinn. Between
two messages its process waits on its input: no slot, no token. It keeps its gates and its serves (`djinn serve run`)
while it is lit. Djinn puts it out once the developer left Djinn alone for its idle delay (30 minutes by default),
warning them 5 minutes before.

**When to light one.** The developer wants to iterate live: changes they judge by eye on a front ("make the button
primary, now 8 px more margin"), or a stack to try by hand ("start the single-process stack so that I try it"). An
answer that asks for it ("Light an ifrit so that I try it") is turned into one by you, as any answer is turned into
tasks.

**When not to.** Work a task does alone with its checks: a fix, a feature, a refactor, an investigation. A task is
cheaper, runs beside others and is checked before it ends; an ifrit waits for the developer.

**One lit per wish.** Before you light one, look at the wish's running tasks: one says `ifrit`. Relay to it rather
than light a second.

**Relaying.** Each request of the developer about its work goes to it as one short, concrete message:

| The developer says | You run |
| --- | --- |
| "The button should be primary" | `djinn task send W12 "Make the Save button of /reviews primary."` |
| "Does the API answer on the stack?" | `djinn task send W12 "Check that the back answers on 8080 and say how to call it."` |
| "Use the demo token?" (a choice) | `djinn question ask "🧭 …" <wish> --about W12 --options "…"` |

Never spawn a task for a change the ifrit can make while it burns, and never repeat its answer to the developer, who
follows it in the window, in its band and its events. Ask a question only for a decision, with `--about` so that it
shows in its band.

**Its gates.** Its serves may hold a gate (`--gate stack`) as long as it is lit. When the brief says a task waits for
a gate it holds, ask the developer whether to finish it or stop it: the waiting task starts once it is out, done or
stopped.

**Out.** The lead watch says "W12 (ifrit) is out: …". Out, it keeps its worktree, branch, session and serves list; a
message lights it again and starts its serves first. Never send it one, and so relight it, without the developer's
word: they left, a relit ifrit would wait for nobody.

**Finishing.** Once the developer says the work is good: `djinn task done W12 --note "…"`. Its input closes, its
serves stop, its gates go back, and its work integrates like a task's, through the commit checks. Stopping it
(`djinn task stop W12`) keeps its work without integrating it.

**Why:** decision Q11 (T51): the developer talks to the Djinn, the Djinn relays to the ifrit; a second ifrit, a
task in its place or a repeated answer costs tokens and makes them follow two threads.

**How to apply:** read this note before a spawn with `--kind ifrit`, and whenever the brief shows a lit ifrit.

---
name: planned-options
description: Plan each option's task when you ask (--plan-file); a one-click answer launches it, a note or a dependency comes back as a revision you settle with djinn question launch
type: feedback
---

When an option of a question leads to a task, plan that task with the question: the developer's one click then
launches it, with no round through you. Give one plan per option that has a task, in a file each:

~~~
option: A
title: Port the store to SQLite
part-of: T07
after: W12
provider: claude

Port internal/store to SQLite, keep the JSON reader for one release. Run the commit checks and the store's tests.
~~~

The header keys are djinn task spawn's flags (`option`, `title`, `project-id`, `provider`, `model`, `part-of`,
`after`, `blocks`, `tilasm`, `kind`); the prompt follows the blank line. `djinn question ask … --plan-file a.md
--plan-file b.md`; `djinn question revise <question> --plan-file b.md` replaces B's plan, and a file holding only
`option: B` removes it. Djinn refuses a plan that could never launch (an unknown task, a started task it blocks).

**Why:** decision Q25 (16/10/2026): an answer the Djinn only turned into a task later cost a round trip for a
decision already taken.

**How to apply:**
- An answer without a note, on a plan with no `after` and no `blocks`: Djinn launches it at once, its decision the
  question, and your watch says `Q12 answered A — "…". W40 launched as planned.` Nothing to do but follow it.
- A note, or a plan with `after` or `blocks`: nothing launches. The event's lines under it give the question, the
  answer and its note, the plan, and how each task it names stands now (done, failed, cancelled, running, changed
  since the question). Read the note against the plan, then settle it in one command: `djinn question launch Q12` as
  it is, or with spawn's flags in place of the plan's (`--prompt "…"`, `--after W41`, `--after ""` for none). When the
  note changes the work beyond flags, ask again rather than guess (Understand an answer before you act on it).
- A plan that no longer holds (its azima closed, a task it comes after cancelled, a task it blocks started) never
  launches: the event says why, and `djinn question launch` refuses it until the flags change what fails.
- An option without a plan is yours to turn into tasks, as before.

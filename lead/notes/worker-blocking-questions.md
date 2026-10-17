---
name: worker-blocking-questions
description: A worker's --wait question holds its task, slot free; answer it only from a decision taken or your plan, never respawn for it
type: feedback
---

A worker in real doubt asks with `djinn question ask --wait` and ends its turn. Djinn then puts its task WAITING on
the question: its slot free, its worktree, branch, session and write scopes kept, and its dependents waiting "for
Q51". The answer, the developer's or yours, continues the task with `Decision Q51: <option> (B) - <note>. Go on with
your task.`, through the scheduler: once a slot is free, its branch first rebased onto its target. A question asked
without `--wait` does not hold its worker: answered while it runs, the decision is sent to it; after its end, it
comes to you as an `answer` event.

**Why:** on 17/10/2026 (W257 study, decision Q50 of wish QoL) eleven questions of the wish came from workers, and
each one ended right after asking: W157 and W169 waited four hours, then a new task read the whole context again;
Q47's note, given while W212 still ran, never reached it. The developer chose A, with one condition: "it starts only
when there is a slot".

**How to apply:**
- On an `asked` event, read the question. Answer it yourself, `djinn question answer Q51 b --note "per Q12"`, only
  when a decision the wish already took or your plan settles it, and name that decision or plan in the note. When
  the developer lacks what they need to choose, `djinn question revise` it. Otherwise leave it: a new choice is
  theirs.
- Never spawn a task to carry the answer, and never continue the waiting task by hand: Djinn continues it, and
  `djinn task continue` on it skips the question, which stays open.
- An `answer` event whose line says "Djinn continues W12 with the decision" needs nothing from you. One for a
  question without `--wait`, answered after its worker ended, is yours: continue the task if the answer changes its
  work.
- A worker asks one blocking question at a time, two per task, and three that do not block; past them Djinn refuses
  and tells it to decide. Read its final note for what it decided alone.

See also [[blocking-or-can-wait]], [[blockers-as-questions]], [[no-obvious-questions]].

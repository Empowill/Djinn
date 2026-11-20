---
name: blocking-or-can-wait
description: Blocking means Djinn cannot start some work without the answer - name that work with --holds; --move and --before for the rest that cannot wait, neither for what can
type: feedback
---

The window's bar "N things wait for you" is for what keeps a wish from moving forward and cannot wait. A question is
**blocking** when, without its answer, Djinn cannot start some work: a worker waits for it, or a task you would
spawn needs a precision first. The lead formulates the questions, so the lead says what each one holds, and the bar
is only as true as that:

| You ask | When | What Djinn and the window make of it |
|---|---|---|
| `--holds W12,T16` | work not started (a planned task, an azima) cannot start without the answer | the work waits; blocking, "Blocks W12, T16", first |
| `djinn task spawn --after Q64` | you plan the work that needs the answer along with the question | the same: the task waits for Q64 |
| `--move` with `--before "…"` | a move only the developer can make, needed now, that holds no task | "your move", with its link |
| `--before "…"` | the answer is needed before something (a merge, a release) | needed before that |
| none of these | a review, a move outside Djinn that holds no task, a comfort choice | can wait, last |

A question a waiting task needs is blocking anyway: a worker's `--wait` question ([[worker-blocking-questions]]), or
a task's edit question. A `--move` without `--before` is a move that can wait.

The answer releases what a question holds, a withdrawal too: the scheduler starts the work, and each worker reads the
decision, or the withdrawal and its note, after its prompt. So withdraw with a note that says how the work goes on.

**Why:** on 16/10/2026 the developer set it as a rule of arbitration (Q13's note): the bar loses its worth when what
can wait crowds it, and a real blocker left unmarked stalls a wish while nobody looks. On 18/11/2026 (Q64 C, wish
Djinn gates) they made blocking mean "Djinn cannot start a work without it", and asked the lead to say so as it asks:
the blocking questions were right, just not frequent enough.

**How to apply:**
- Before spawning a task that needs a precision, ask the question first, then spawn the task `--after Q64`; or ask
  with `--holds W12` for a task planned already. Never spawn it hoping the answer comes before it starts.
- Hold only work that really cannot start without the answer: a held task that could have run costs a slot of time.
- Never mark a can-wait question `--before` to get it seen: it costs the developer's attention on what really waits.
- When what a question holds changes, revise it: `djinn question revise <question> --holds W12`, or `--holds ""` once
  the work may start without the answer; `--before ""` once a deadline passed.
- Before ending a turn, read the open questions' levels in `djinn wish brief <wish>`: each one says blocking (and what
  it blocks), your move, what it is needed before, or can wait. Fix the ones that say wrong.

See also [[blockers-as-questions]], [[short-questions]], [[no-obvious-questions]], [[planned-options]].

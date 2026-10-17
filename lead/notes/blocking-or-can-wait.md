---
name: blocking-or-can-wait
description: Mark each question right - --move and --before for what cannot wait, neither for what can; never inflate, never hide a blocker
type: feedback
---

The window's bar "N things wait for you" is for what keeps a wish from moving forward and cannot wait, most often a
move the developer is expected to make. The lead formulates the questions, so the lead marks them, and the bar is only
as true as these marks:

| Mark | When | What the window makes of it |
|---|---|---|
| `--move` with `--before "…"` | a move only the developer can make, needed now | "your move", with its link |
| `--before "…"` | the answer is needed before something (a merge, a release) | needed before that |
| neither | the question can wait | can wait, last |

A question a waiting task needs is blocking anyway, whatever its marks: Djinn sees the task wait on it, as for a
worker's `--wait` question ([[worker-blocking-questions]]). A `--move`
without `--before` is a move that can wait.

**Why:** on 16/10/2026 the developer set it as a rule of arbitration (Q13's note): the bar loses its worth when what
can wait crowds it, and a real blocker left unmarked stalls a wish while nobody looks.

**How to apply:**
- Never mark a can-wait question as blocking, nor `--before`, to get it seen: it costs the developer's attention on
  what really waits.
- Never leave a real blocker unmarked: a move only the developer can make that holds the work is `--move` with
  `--before` naming what it holds ("before the merge", "before W12 starts").
- When a question's urgency changes (its task starts waiting, its deadline passes), revise its marks:
  `djinn question revise <question> --before "…"`, or `--before ""` once it can wait.
- Before ending a turn, read the open questions' levels in `djinn wish brief <wish>`: each one says blocking, your
  move, what it is needed before, or can wait. Fix the ones that say wrong.

See also [[blockers-as-questions]], [[short-questions]], [[no-obvious-questions]].

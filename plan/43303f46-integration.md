---
id: 01a12257-92a9-704b-9ef4-46c843303f46
code: T30
phase: 2
status: open
after: T07
---

# T30 · Djinn integrates finished work by itself

**Goal.** A worker's work counts once it is in the wish's branch, tested, not when its worker ends. Djinn brings it
there by itself: the lead no longer merges, regenerates, tests and installs by hand, and the person is asked only
what is theirs to decide.

**The developer's words.** "Why is it you who coordinates, merges, runs the tests and all, when the Go orchestrator
is supposed to do all that?" Today Djinn schedules, runs, resumes, gates and measures the workers; then each one's
work sits on its branch, and the lead integrates it by hand (`git merge`, a generated file regenerated, the tests
through a gate). It costs: nothing moves while the lead is away (W107–W110 waited through a session limit), a task
that waits for another starts on a branch without its work (W112 and W113 started before W111 was merged), and the
lead spends its tokens on mechanical work.

## What is decided

- **The deterministic part is Go, no model.** When a worker ends done, Djinn merges its branch into the wish's
  integration branch (`feat/wails-go` here: a setting of the wish in its project), in a worktree of its own, never in
  the person's checkout. A conflict only in generated files (a project setting: their paths, and the command that
  makes them, `go tool task gen` here) is settled by making them again. Then the project's test command (a project
  setting) runs through a gate; green, the integration branch moves on. The person's checkout of that branch, when
  clean and behind, follows by a fast-forward; otherwise Djinn says so and leaves it.
- **Judgement goes to a worker, not to the lead.** A conflict in code, or red tests, starts a worker of correction on
  the merge, with what failed, part of the same azima; its work integrates the same way. After a few attempts
  (a setting), Djinn asks the person a question.
- **When to commit** (the developer, 09/10/2026): Djinn commits the finished work into the integration branch when
  an azima ends, or once an hour has passed **and** three tasks are done since the last commit, whichever comes first.
  A task done waits for that batch, its work tested with the others'. One exception, so that the graph never stalls:
  a task another task waits for is committed at once, alone. The hour and the count are settings of the wish.
- **A task waits for its dependencies to be integrated**, not only done, and its worktree starts from the integration
  branch, so it builds on their work.
- **The person decides what is theirs**: to install and restart on the new build (Djinn proposes it, with what
  changed and what to check), and the choices no worker can make.

## Done when

- [ ] A task done integrates by itself: merged in its own worktree, generated files made again, tests through a gate,
  the branch moved on, the person's clean checkout fast-forwarded; each step in the task's events and status.
  (needs: the tests)
- [ ] The commit cadence: at an azima's end, or after an hour and three tasks done since the last commit; at once for a
  task another waits for. (needs: a test with a fake clock)
- [ ] A code conflict or red tests start a correction worker, part of the same azima; after N attempts, a question.
- [ ] A task waits for its dependencies to be integrated, and starts from the integration branch.
- [ ] The window, the page and the brief show where each task's work stands (done, integrating, integrated,
  conflict, red), and propose installing once a batch is integrated.

## Tests must be fast

No real sleep, fake clocks, milliseconds: a test over 1 s is a bug. Merges and conflicts on a real Git repository in
a temporary folder, with a fake test command.

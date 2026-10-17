# Dependencies

A dependency says that one piece of work comes after another: a task starts only once the tasks it comes after are
done and their work committed, and it starts from that work. Dependencies are the only thing that makes a task wait
for another one; together, they make a wish's plan a graph.

## Why dependencies

Workers run side by side, each in a worktree of its own. That is fast, and it is only safe when the work in parallel
does not need each other's result. A dependency is how the plan says it does:

- **Order where it matters.** "Write the migration" before "Use the new column": the second worker starts on a
  checkout where the column exists.
- **Work that builds on work.** A dependent task does not start from a stale copy of the project: its worktree starts
  from the wish's branch, once the work it needs is committed there.
- **Everything else runs at once.** Two tasks with no dependency between them may run together, as the machine
  allows. The plan stays as parallel as it can be.

## The plan as a graph

The tasks of a wish, and its [azimas](#azima), form a graph: each one points to what it comes after. The graph has no
cycle. Djinn refuses any change that would close one, names the loop it would make, for instance W1 → W3 → W1, and
changes nothing. A task cannot come after itself either.

Two kinds of link live in that graph, and they are not the same:

| Link | What it says | Does it make work wait? |
| --- | --- | --- |
| Comes after | This task needs that one done first. | Yes |
| Part of | This task belongs to that azima's goal. | No |

**Being part of an azima never makes a task wait.** A task part of an azima that comes after another azima still runs
as soon as its own dependencies allow. A task waits for an azima only when it comes after that azima itself: it then
waits until the azima is done, that is validated, and its card says it waits for the azima to be done.

When a project keeps its plan in files, one per azima, each file also carries what its azima comes after. Djinn writes
that line from the plan it holds, so that Git carries the graph; a new azima read from a file takes its file's line
once, and from then on the plan Djinn holds is the one that counts.

## What a dependency waits for

A task whose dependency is not ready stays planned, and its card says why, naming the first task that holds it. The
code in the reason is a link to that task's card.

| The task it comes after is… | What the dependent does |
| --- | --- |
| Planned, running, interrupted or resuming | Waits for it |
| Done, its work being merged | Waits for that work to be committed in the wish's branch; Djinn starts that commit at once |
| Done, its merge in conflict or its checks red | Waits, and the reason says conflict or red |
| Done with nothing to merge, or its work dropped on purpose | Starts |
| Cancelled | Starts: a cancelled task holds nobody, and the dependent is told it was cancelled |
| Failed | Waits while it is failed, until it is continued or marked done |
| Stopped | Fails, saying its dependency ended stopped |

When a failed or stopped dependency is marked done, cancelled or continued, the tasks that had failed because of it
are planned again, and start when they can.

**Start anyway**, on a task that waits for the machine, starts it beyond the load bar; it does nothing for a task that
waits for a dependency.

## Where its work starts

A task's worktree starts from the tip of the wish's integration branch in its project, which holds the committed
work of every task it comes after. Under the per-azima [push strategy](#push-strategy), it starts from its azima's own
branch instead. If the work of a dependency has not reached that branch yet, no worktree is made: the task waits,
saying so.

The branches of the dependencies are never merged into the new worktree one by one; Djinn waits until their work is
in the branch it starts from. See [Integration branch](#integration-branch).

## Other reasons to wait

A dependency is one reason among others. A planned task may also wait because:

- **its wish is paused** or granted;
- **no slot is free**, the machine is under pressure, or memory is short: the card says **Waits for the machine**,
  with the reason, and offers **Start anyway**;
- **its provider's usage limit** is reached: **Waiting for the limit**;
- **the wish's branch is red** after its push checks, until it is green again;
- **a merge of the wish is in conflict or red**, and its correction repairs the branch: the task waits until the
  correction's work is in, so that it starts from the repaired branch
  ([One repair at a time](#merges-and-corrections-one-repair-at-a-time));
- **its write scope is taken**: outside Git only, two tasks of a project whose folders overlap never run together.

These waits are about the machine and the moment, not about the plan: they never make a dependency.

## Overlaps: what runs on the same files

Two workers that change the same file side by side make a merge conflict, and a correction worker to repair it. The
Djinn owns each wish's graph, so it checks before it sends a worker:

- **What runs and what comes.** The task list command, with its open filter, gives the running, paused, resuming,
  waiting and planned tasks of a wish, without the past; its status filter keeps the states named.
- **Which files each open task touches.** The task overlaps command gives, for each open task, the files it changed
  so far (its worktree against the commit it started from, untracked files included), or, for a planned task, the
  paths its write scopes name; a planned task without write scopes shows its paths as unknown. Then it pairs the tasks
  that touch the same files. Generated files are left out: Djinn makes them again on a conflict. The wish's brief
  carries the same overlaps, short.

New work on files an open task touches joins that task (its prompt while it is planned, an instruction while it
runs), or comes after it; it never runs beside it. Big work goes as a chain of small steps, each merged before the
next. Work on other files still runs side by side.

> **Example.** Two tasks changed the same screen side by side, and a third had changed it too; both merges conflicted,
> and two correction workers repaired them. The second task coming after the first would have merged clean.

## Seeing the graph in the window

In the wish's **Tasks** tab, **How the tasks show** has two layouts:

- **List: what moves first, then the azimas.** A planned task's card says why it waits, and adds after which tasks it
  comes. An azima's row says **Waits for** the tasks and azimas it still waits for, until it is done.
- **Tree: every task, oldest at the bottom.** A row per task, grouped by azima, with a thin line from each task to
  what it comes after; hovering a row brings out its links. This is the plan drawn as a graph. An azima has no row of
  its own, so a dependency on an azima draws no line. Left of the graph, each row says to the second when its task
  first started, or when it was created if it never started, in your language and time zone; the day shows only where
  it changes from the row above. Hovering the row says which of the two it is, and when the task ended.

In **What runs**, beside the questions of a wish and of the flying carpet, a task that waits on another sits under it
when that one waits too, or names it.

The window shows dependencies; it does not change them. There is no drag to link two tasks. **Mark done**, on a task
no worker runs, is the one gesture there that frees what waits on it.

## Changing dependencies from the Djinn

The Djinn lays out the dependencies when it plans, and keeps them true as the plan changes. Ask it in words:

> **Example.** "W12 should wait for the schema work in W9." The Djinn makes W12 come after W9. W12, still planned,
> now waits for W9 to be done and committed, and starts from a branch that holds it. Had W12 started already, the
> Djinn would tell you it cannot: a running task's dependencies change once it ended, and a task that ran keeps the
> dependencies it ran with.

What the Djinn can do, and what Djinn refuses:

- **Give a new task what it comes after**, as it spawns it.
- **Put a new task before planned ones**, in the same step, so that none of them starts in between. A task that has
  started cannot be put after a new one.
- **Replace what a planned task waits for**, all at once or not at all, for several tasks together.
- **Group a task into an azima**, or take it out: a grouping, never a wait.
- **Refused:** a link that closes a cycle; a new task after a task that ended stopped, which would never start; and,
  under the per-azima push strategy, any link that makes one azima wait for another, directly or through their work.

## Good to know

- **Say the order, not the timing.** "B after A" is a dependency; "do A first" in a prompt is not, and the scheduler
  will not read it.
- **Group with azimas, order with dependencies.** Putting tasks in the same azima does not sequence them; ranking an
  azima higher only starts its waiting work first.
- **Few dependencies, more speed.** Each one makes a task wait; add those the work needs, no more.
- **A failed dependency holds its dependents.** Look at the failed task first: continue it, or mark it done if its
  work is there, and they go on.
- **Marking done is not cancelling.** A task no longer useful is cancelled; one whose work is done is marked done, and
  its work is merged before its dependents start.

## Read next

- [Task and worker](#task-and-worker): the work that waits and starts.
- [Azima](#azima): the goals the plan's work is part of.
- [Integration branch](#integration-branch): where a dependency's work is committed.

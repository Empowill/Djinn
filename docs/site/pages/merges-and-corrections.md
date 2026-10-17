# Merges and corrections

When a worker finishes, its work is still on its own branch. Djinn brings it into the wish's
[integration branch](#integration-branch) by itself, with no model: it merges the task's branch, runs the project's
checks on the result, and moves the branch only when they pass. When the merge conflicts or a check is red, Djinn
starts a **correction worker** to fix it.

This happens in every project whose [settings](#project-and-settings) name at least one check.

## Why Djinn merges

A worker's work counts once it is in the wish's branch, checked:

- **The next tasks build on it.** A task that comes after another starts from the branch once that task's work is in.
- **Broken work never lands.** The checks run on the merge itself, the task's work together with everything merged
  before it, not on the worker's branch alone.
- **No model for the routine.** Git merges, the checks judge; a model is called only when something has to be fixed.
- **Nothing lost, nothing hidden.** Each task says where its work stands, and what failed when it failed.

## How a task's work lands

1. **The worker ends done.** Its work is **waiting to be committed**.
2. **In batches.** Djinn takes the finished tasks waiting for the same branch together, in the order they ended: one
   **batch**, checked once. A task that ends while a batch is checked goes with the next one.
3. **It leaves out work with nothing of its own.** A task whose branch holds no commit beyond the one it started from
   is not merged: its end says *nothing to merge*. A merge that would change nothing is undone: Djinn never makes an
   empty merge commit.
4. **It looks for work left behind.** A worktree that still holds changes not committed is not merged: see
   [Work left not committed](#merges-and-corrections-work-left-not-committed).
5. **It merges** the tasks' branches into the integration branch, one on top of the other, in its own worktree under
   its data folder, never in your folder. A merge commit records each branch. A branch that conflicts **leaves the
   batch**: its merge is undone, the others go on without it, and it merges alone right after them.
6. **It makes the worktree ready**, when the project names a setup, such as installing dependencies, and something it
   depends on changed.
7. **It runs the commit checks** once for the batch, each through the [gate](#gates) of its name, in the order the
   settings give them.
8. **Green**: the branch moves to the merge. The batch's tasks are **committed**, with its commit; their worktrees are
   removed, their branches kept. Your checkout of the branch follows when it is clean.
9. **Red, or a conflict**: the branch stays as it was, the tasks say what failed, and a correction worker starts: one
   for the whole red batch, or one for the branch that conflicts. While it works, nothing else merges into the branch:
   see [One repair at a time](#merges-and-corrections-one-repair-at-a-time).

Djinn runs this every minute, and at once when a task ends. The push checks never hold it up: they run apart, on a
commit of their own, and a merge that lands meanwhile only has them run again on the new tip (see [Push](#push)).

> **Example.** W3, W4 and W5 end a few minutes apart. Djinn merges them as one batch into feat/invoice-pdf; W4 touched
> the same lines as W3: its merge conflicts, and it leaves the batch. W3 and W5 pass the lint together, the branch
> moves, both show **Committed into feat/invoice-pdf as 1a2b3c4d**. W4 merges alone after them, conflicts again, and
> shows **Merge being fixed by W6**; W6, a correction worker, starts on that very merge. W7, which ends meanwhile, waits
> for W6. Ten minutes later W6's work passes the lint, W4 shows **corrected by W6**, and W7 merges.

### A check the worker already passed

A worker runs the commit checks before it ends. When its last run of a check, through its gate, passed on the very tree
the merge produces, Djinn does not run it again; the task's events say the check passed by the worker on this tree. Any
difference, a merge that brings other work for instance, and the check runs.

### Generated files

A project may name the files its code generation makes, and the command that makes them. A conflict **only** in those
files is not a real conflict: Djinn takes the task's side, runs the generation again through its gate, and commits the
result. No correction worker is needed.

## Where you see it

Each finished task says where its work stands, in the Tasks tab, on its card and its line:

| What the task says | What it means |
| --- | --- |
| **Merging** | Its work waits to be merged, or is being merged. |
| Waiting to be committed | In line, with why it waits when something holds it: a correction repairing the branch, your checkout, the remote, Git's identity. |
| Being committed into feat/invoice-pdf | Djinn is merging it and running the checks. |
| Committed into feat/invoice-pdf as 1a2b3c4d | In the branch, checked. |
| **Merge failed** | A conflict or a red check, and no correction runs. |
| Conflict, not committed | The merge conflicts: in which files, with which branch. |
| Red tests, not committed | A commit check failed: its command, and the end of its output. |
| **Merge being fixed by W6** | A correction worker is on it. |
| corrected by W6 | Committed, thanks to W6's correction. |
| **To review**, Changes not committed, not merged | Its worktree held changes no commit took: a review worker sorts them out. |

The top of the wish counts them: **merging**, work waiting to merge with no worker running, and **being fixed**, merges
an agent corrects, with nothing for you to do.

Beside the questions, **What runs** follows it as it moves: the batch being merged, its check and for how long, the
work waiting in line, a push its checks hold and the tests that failed, and the repair workers, a wrench in place of
their dot. See [The window](#the-window).

Once committed, each task's work shows in the **Review** tab, a line per task, to read and mark reviewed. See
[Review](#review).

## Correction workers

A correction worker is a work task Djinn starts by itself, in the same azima as the task that failed, on the same agent
and model. Its worktree is the failed merge itself:

- **For a conflict**, the task's branch merged again, its conflicts left in place for the worker to settle.
- **For a red check**, the merge as it was, the check failing on it.

Its first instructions say what failed: the files in conflict, or the check's command and the end of its output. It is
told to commit its work. Its branch then lands like any task's, checked; when it passes, the failed task is committed
with it, **corrected by** the correction.

### A red batch

A batch whose commit checks are red gets **one** correction worker, which sees all of it: its first instructions name
every task of the batch, the check's command and the end of its output. Its success brings the whole batch in.

When that correction fails, Djinn goes **step by step**: it merges the batch's work one branch at a time, each checked
on its own. The branches that pass go in; the first that turns the checks red is the culprit, and gets a correction of
its own. The work of the batch's failed correction is left out. A project whose settings allow no correction goes step
by step at once.

### One repair at a time

A correction **repairs** the branch, whatever failed: a merge's conflict, its commit checks, the push checks, or main's
merge. One repair runs on a branch at a time:

- **The branch is frozen.** While the correction works, and until its work is in, nothing else merges into the branch,
  main neither. The work that ends meanwhile waits in line, each task saying *waits for W6, which repairs
  feat/invoice-pdf*. The correction thus works on a branch that does not move under it.
- **Its work goes first.** Once it ends, the correction's work merges alone; then the line merges as one batch, checked
  once.
- **A new failure waits its turn.** Push checks that go red while a correction runs keep the push held, and start no
  second correction; work you ask Djinn to correct again waits for the one that runs, then merges alone.
- **It starts on a red branch.** A correction never waits for the branch to be green: it is what makes it green again.
- **It takes a place at once.** A correction, and a review of work left not committed, go before every planned task,
  and never wait for a free place: on a full machine, Djinn pauses the running work task that comes last (the last wish,
  then the most recently started), gives its place to the correction, and resumes that task once the correction ends.
  It never pauses another correction or review, an urgent task, an investigator, or a task started by hand.
- **New workers wait.** While a merge of the wish is in conflict or red and its correction repairs the branch (it waits
  to start, runs, or its work waits to go in), no new worker of the wish starts in that project: it would start from
  a branch without that work, and its own merge would conflict in turn. Each says *waits for W225 to settle the
  conflict in feat/qol* (or *to repair the red checks of*, *to settle the merge of main into*). The correction itself,
  reviews, an **urgent** task and the investigators of questions still start, and the workers already running go on.
  Once the correction's work is in, the work that waited starts, from the repaired branch.
- **Running workers take the repaired branch.** Once the repair's work is in, checked, and no other correction repairs
  the branch, each running worker of the wish whose branch lacks the new work gets one message: merge the branch now,
  its commits listed, settle any conflict with your own changes in mind, run the tests you touched, then go on. Djinn
  never merges in a worker's folder, and never writes to it in the middle of a step: a worker running a command gets
  the message once the command has returned. A merge with no repair before it tells no worker: they take that work
  when they end.

### When corrections run out

A project's settings say how many correction workers Djinn tries on one failure: **two** by default, none at all if
set to zero. A correction whose worker fails, or whose own work conflicts or tests red, counts as an attempt.

Past the last attempt, Djinn stops and hands the failure to the Djinn: the work of W4 into feat/invoice-pdf failed, and
the move is the Djinn's: retry a correction, drop the work, or leave it waiting. The Djinn decides, and asks you when it
needs you. The rest of the wish goes on meanwhile: the other tasks merge without it.

### Stopping a correction

A correction worker you stop may be continued, on another agent for instance, within ten minutes. After that it no
longer corrects the merge: the task stays **Merge failed**, and the Djinn decides what follows.

## Work left not committed

Djinn commits nothing blindly. Before merging a task, a correction's included, it looks at the task's worktree: files
changed or added that no commit took, ignored files aside.

When there are some, the task says **To review**: its changes are not committed, not merged. A **review worker** starts
by itself, in that task's worktree and on its branch, with the task's instructions, the files and their changes. It
commits what belongs to the task, in the repository's style, removes the rest (debug output, scratch files, build
leftovers), and ends saying what it kept, what it dropped, and why. Its branch then lands like any task's, and the task
says **reviewed by** that worker.

A review counts as an attempt, as a correction does.

## When Djinn does not merge

- **A project that names no check.** Djinn merges finished work only into a wish's own branch, untested, never into the
  project's main branch. Elsewhere, the work is left out of any branch, and the task's events say why.
- **Your checkout has changes not committed** on the integration branch: the work is checked, and waits for you to
  commit or put aside your changes before the branch moves. See [Integration branch](#integration-branch).
- **The remote's copy of the branch holds commits it lacks**: nothing more merges until the branches are aligned by hand.
- **Git has no identity.** Djinn merges and commits as you, with your Git name and email, and never invents one: the
  work waits, saying how to set them.
- **The settings file cannot be read**: merges in the project wait until it is fixed.

## Good to know

- **Name a fast check before a commit.** It runs on every task's merge: a lint or a quick build, not a suite of half an
  hour; keep the long one for the push. See [Gates](#gates).
- **Workers should run the commit checks before they end.** Djinn tells them which, and a check passed on the same tree
  is not run again: the merge goes faster.
- **A correction is a task like any other.** It shows in the Tasks tab, part of the same azima, and you can follow it,
  send it a word, or stop it.
- **Uncommitted work is not lost.** It is reviewed, not dropped: a review worker decides what belongs to the task.
- **A failed merge holds the branch while it is repaired.** The tasks that depend on it wait; the work that ends
  meanwhile waits in line, and merges as one batch once the correction is in.

## Read next

- [Integration branch](#integration-branch): where the merged work lands.
- [Gates](#gates): how the checks share the machine.
- [Push](#push): when the merged work reaches the remote.

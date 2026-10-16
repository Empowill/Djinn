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
2. **One at a time.** Djinn takes the finished tasks in the order they ended, each one alone, so a red check always
   names the task that brought it.
3. **It looks for work left behind.** A worktree that still holds changes not committed is not merged: see
   [Work left not committed](#merges-and-corrections-work-left-not-committed).
4. **It merges** the task's branch into the integration branch, in its own worktree under its data folder, never in your
   folder. A merge commit always records it.
5. **It makes the worktree ready**, when the project names a setup, such as installing dependencies, and something it
   depends on changed.
6. **It runs the commit checks**, each through the [gate](#gates) of its name, in the order the settings give them.
7. **Green**: the branch moves to the merge. The task is **committed**, with its commit; its worktree is removed, its
   branch kept. Your checkout of the branch follows when it is clean.
8. **Red, or a conflict**: the branch stays as it was, the task says what failed, and a correction worker starts. The
   tasks that end after it are merged on their own meanwhile.

Djinn runs this every minute, and at once when a task ends.

> **Example.** W3 and W4 end a few minutes apart. Djinn merges W3 into feat/invoice-pdf: the lint passes, the branch
> moves, W3 shows **Committed into feat/invoice-pdf as 1a2b3c4d**. W4 touched the same lines: its merge conflicts. W4
> shows **Merge being fixed by W6**, and W6, a correction worker, starts on that very merge. Ten minutes later W6's work
> passes the lint, and W4 shows **corrected by W6**.

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
| Waiting to be committed | In line, with why it waits when something holds it: your checkout, the remote, Git's identity. |
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

### When corrections run out

A project's settings say how many correction workers Djinn tries on one failure: **two** by default, none at all if
set to zero. A correction whose worker fails, or whose own work conflicts or tests red, counts as an attempt.

Past the last attempt, Djinn stops and hands the failure to the Djinn: the work of W4 into feat/invoice-pdf failed, and
the move is the Djinn's: retry a correction, drop the work, or leave it waiting. The Djinn decides, and asks you when it
needs you. The rest of the wish goes on meanwhile: the other tasks merge on their own.

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
- **A failed merge blocks only what depends on it.** The tasks that come after it wait; the others go on.

## Read next

- [Integration branch](#integration-branch): where the merged work lands.
- [Gates](#gates): how the checks share the machine.
- [Push](#push): when the merged work reaches the remote.

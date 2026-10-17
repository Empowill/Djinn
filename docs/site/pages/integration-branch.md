# Integration branch

A wish's integration branch is the branch, in each of its projects, where its finished work lands. Every worker starts
from it, Djinn merges each task's work into it once checked, and Djinn pushes it. It is the one branch that holds
everything the wish has done so far in that project.

Under the per-wish [push strategy](#push-strategy), the default, a wish has one integration branch per project. Under
the per-azima strategy, each azima has a branch of its own instead, and the wish's integration branch only takes the work
that belongs to no azima.

## Why an integration branch

Workers run side by side, each in a worktree and on a branch of its own, so that none steps on another. Their work has
to come together somewhere before anyone else sees it:

- **One place where the work adds up.** The tasks that come after a task start from the integration branch, so they
  build on its work, not on a copy of main from yesterday.
- **Checked before it counts.** Djinn merges each task's work into the branch only once the project's checks pass on
  the result. See [Merges and corrections](#merges-and-corrections).
- **One push for many tasks.** Djinn pushes the branch, not each worker's: one CI run per push instead of one per
  worker. See [Push](#push).
- **Your checkout stays yours.** Djinn merges in a worktree of its own, never in your folder.

## Which branch it is

When you make a wish, Djinn records, in each Git project of the wish, the branch your checkout is on: if you were on
feat/invoices, the wish's work lands in feat/invoices. A project added to the wish later gets its branch the same way.

When the checkout was on no branch at that moment, the wish has none yet: the first merge takes the branch the checkout
is on then. If it is still on no branch, the work waits, saying so, until you name one.

### The chip at the top of the wish

The top of the wish shows where its work lands, one chip per Git project: **Lands in feat/invoices**, the project's name
first when the wish has several, or **Where it lands** when none is set yet. Its tooltip says that Djinn merges the
wish's finished work into that branch, then pushes it.

A click opens the branch picker:

- **The project's local branches**, the latest commit first, eight at a time; typing narrows them. The branches Djinn
  made for its workers are left out.
- **A new branch**: type a name no branch has, and the picker offers **New branch feat/pdf from feat/invoices**. When
  the remote already holds a branch of that name, an open pull request's for instance, Djinn makes it from the remote's,
  not from the current branch.

The window then says **The wish's work now lands in feat/pdf.** The next workers start from it, and the next merges go
into it. Work already merged stays where it is.

Djinn refuses the change while a merge into the current branch is under way, naming it; change it once that merge has
ended.

Under the per-azima strategy the chip reads **Each azima lands in main**, and does not open a picker: each azima has its
branch, shown on its card. See [Push strategy](#push-strategy).

You can also ask the Djinn to change it, in its terminal or with **Write to the Djinn**.

> **Example.** You make the wish "Let customers export their invoices as PDF" while your checkout of app is on main.
> Before the first task ends, you open the chip, type feat/invoice-pdf, and pick **New branch feat/invoice-pdf from
> main**. Every task's work now lands there, checked, and Djinn pushes it there, never to main.

## Main is a branch like any other

The integration branch is whatever branch your checkout was on, main included. In a project that names checks, Djinn
merges a wish's checked work into main when main is its integration branch, and pushes main by the project's cadence.

In a project that names no check, Djinn never merges untested work into main: it merges only into a wish's own branch,
untested. A wish there whose branch is main has its work left out of any branch; its tasks' events say why, and a task
that comes after such work waits for it to reach the branch.

If your team does not push straight to main, give the wish a branch of its own before its first task ends.

## How workers use it

Each worker of the wish starts its worktree from the integration branch's tip, as it is when the worker starts: its
start event names the branch and the commit, as in **from feat/invoice-pdf at 1a2b3c4d**.

A task that comes after another waits until that task's work is **committed** into the branch, not only finished: it
says **waits for W5 to be committed**. That way it starts on top of W5's work. See [Dependencies](#dependencies).

## Your checkout follows

Djinn merges in its own integration worktree, under its data folder, never in your folder. When the branch moves:

- **Your checkout is on it and clean**: it follows, as a fast-forward would, and you see the new work in your folder.
- **Your checkout is on it with changes not committed**: Djinn leaves your files alone, and the branch with them. Moved
  under you, the branch would have your next commit undo the work. The task waits, saying that your checkout has
  changes not committed, and the branch moves once you commit them or put them aside.
- **Your checkout is on another branch**: nothing happens in your folder.

## Keeping up with main

While the wish works on its branch, others merge into the project's main branch. In a project that names checks, Djinn
keeps each active wish's branch up with main by itself, with no model:

1. **It looks** at main at most every hour, fetching it from the remote with your own credentials. Your settings may
   say how often, and which branch is main: by default the remote's default branch, else main, else master.
2. **It merges** main in once main holds a **release** the branch lacks, a version tag; your settings may say at each
   commit instead, or never.
3. **It checks** the merge as it checks a task's work, in its own worktree, and moves the branch when the checks pass.
4. **A conflict or a red check** starts a correction worker, as for a task. When the corrections are spent, Djinn tells
   the Djinn, and tries again once main moves.

The top of the wish shows the last merge, **origin/main merged into feat/invoice-pdf: 4 commits, v0.2.0**, with its
commits on hover, and, when the next one waits, **The merge of main waits** and why. The merge reaches the remote with
the next push.

**A merge, never a rebase.** A rebase would rewrite every commit of the branch, which Djinn would then have to push by
force, and Djinn never forces a push: the people who fetched the branch, and its pull request's reviews, would lose
their place. A merge needs no model; a model only settles a conflict, when there is one.

## When the remote moved on

A teammate may push to the branch too. Before Djinn merges work, or main, into a branch, main included, it fetches the
remote's copy of that branch, then:

- **The branch is behind the remote's**, holding nothing the remote lacks: Djinn brings it up to the remote's, a
  fast-forward, and merges the work on top. Your checkout of the branch follows when it is clean.
- **Your checkout of the branch has changes not committed**: Djinn leaves your files alone, and the branch with them.
  The work waits, saying so, and the Djinn hears it; once you commit them or put them aside, the branch catches up and
  the work merges.
- **The branch has diverged**, each side holding commits the other lacks: Djinn merges nothing more into it and pushes
  it no more, as it would have to force the push. The work waits, saying the branch has diverged and how to align them
  by hand: merge the remote's commits into it, or reset it to the remote's if its own are not wanted. The Djinn hears
  it once. Once the branches agree, the work merges again.

Djinn never forces and never rebases. A remote that does not answer within half a minute holds no merge: Djinn then
compares with the remote's copy as Git last fetched it.

You can bring a branch up to its remote yourself: see [Pulling yourself](#push-pulling-yourself).

## Good to know

- **Check the chip before the first task ends.** A wish made while your checkout is on main lands its work in main.
- **Commit or put aside your changes.** A checkout of the integration branch with changes not committed holds every
  merge into it: the tasks show **Merging** and wait.
- **Name a branch Git can check out.** A wish whose project's checkout is on no branch has nowhere to land.
- **Changing the branch moves nothing.** Work already merged stays in the old branch; the next work goes to the new one.
- **Djinn needs a Git identity.** It merges and commits as you; without your name and email set in Git, the work waits,
  saying how to set them.

## Read next

- [Merges and corrections](#merges-and-corrections): how a task's work gets into the branch.
- [Push](#push): when and how the branch reaches the remote.
- [Push strategy](#push-strategy): one branch per wish, or one per azima.

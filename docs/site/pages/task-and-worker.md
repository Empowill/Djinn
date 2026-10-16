# Task and worker

A task is one piece of work in a wish, coded W1, W2 and on. A worker is the agent that does it, Claude Code, Codex or
Antigravity, in a worktree and a branch of its own, so that no two workers step on each other.

On its card in the Tasks tab, you can **Send** a running worker an instruction, **Pause the worker** to free its slot,
**Stop the worker**, or **Mark done** a task no worker runs once its work is done. Workers never commit: Djinn commits
each task's work as it ends.

## Urgent tasks

A task waits its turn: the tasks Djinn resumes and the corrections first, then the ones others wait for, then the
oldest. When something blocks the wish, the Djinn may mark the one task that unblocks it **urgent**: the repair of an
integration branch whose push checks stay red after Djinn's corrections, or the task a stuck chain waits for. Its card
and its line in the tree then carry a small **Urgent** label, and:

- it goes before every task that waits, whatever their age;
- it takes a place even when the machine is full, as **Start anyway** does, and the load bar counts it among the
  workers started by hand;
- it starts while the push checks are red: nothing else could make the branch green again.

One task of a wish is urgent at a time: the Djinn cannot mark a second one while the first is to run or runs. Ask the
Djinn for it; the window has no switch for it. Urgent is for unblocking only, never to hurry ordinary work.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Wish](#wish), [Dependencies](#dependencies), [Merges and corrections](#merges-and-corrections).

# Task and worker

A task is one piece of work in a wish, coded W1, W2 and on: "Add the PDF renderer", "Write the export button's
tests". A worker is the agent that does it, Claude Code, Codex or Antigravity, started by Djinn in a worktree and a
branch of its own, so that no two workers step on each other. One task, one worker.

## Why tasks and workers

A wish is too big for one agent session: its context fills, its attention drifts, and one mistake stalls everything.
Djinn splits the work instead:

- **Small, focused sessions.** Each worker gets one task and the context it needs, nothing else.
- **Work side by side.** Workers run at the same time, as many as the machine allows, each in its own copy of the
  project.
- **Work you can follow.** Each task has its status, its events, its diff and its cost; each ends done, failed or
  stopped, and says so.
- **Work that lands safely.** Djinn merges each task's finished work into the wish's branch, checked, and the next
  tasks start from it.

The Djinn plans the tasks and spawns them; you follow them, answer their questions, and step in when you want to.

## The life of a task

1. **Spawned.** The Djinn spawns a task with a title, a prompt, a project, an agent and its model. It is part of an
   azima or of none, and it comes after the tasks it needs.
2. **Planned.** It waits until it can start: its dependencies done, a slot free on the machine. Its card says why it
   waits, and only while it waits: a task that started never shows a wait.
3. **Running.** Djinn makes its worktree and its branch, from the wish's branch, and starts the worker on its prompt.
4. **Ended.** The worker's turn ends. Done, its work goes in line to be merged; failed or stopped, it stays as it is
   until someone decides. A worker that asked you a blocking question ends its turn but not its task: the task waits
   for your answer, then goes on (see [A worker in doubt](#task-and-worker-a-worker-in-doubt) below).
5. **Merged.** Djinn merges the work into the wish's branch, runs the project's checks, and commits it. The worktree
   goes; the branch stays.

> **Example.** The Djinn spawns W4, "Render invoices as PDF", after W2, which adds the invoice model. W4 waits, its
> card saying so, until W2's work is committed. It starts from a branch that holds that work, runs for twenty minutes,
> ends done, and its work is merged: its card says it was committed into the wish's branch. W6, which comes after W4,
> starts.

## Statuses

The status is one colour, one icon and one word:

| Status | What it means |
| --- | --- |
| Planned | Not started yet: it waits, and its card says why |
| Running | Its worker works |
| Paused | Its worker holds still where it is, and frees its slot |
| Waits for your answer | Its worker stopped on a question only you can answer: one it asked you, or whether it may edit the project. No slot is taken meanwhile |
| Waiting for the limit | Its agent's usage limit is reached: it resumes once the limit resets |
| Resuming | Djinn starts it again by itself, after a restart, a limit or a network outage |
| Interrupted | Cut short when Djinn stopped: Djinn resumes it at its next start, when it can |
| Resumed as | It was continued in another task, named there |
| Done | Its worker ended its turn normally |
| Merging | Done, its work being committed into the wish's branch |
| Merge failed | Done, its work in conflict or red under the checks |
| Merge being fixed by | Done, a correction task fixing its merge |
| To review | Done, with changes left not committed, being reviewed |
| Failed | Its worker ended on an error |
| Stopped | Stopped on request |
| Cancelled | Dropped, its worktree removed, its work out of the merge |
| Watching | A [watcher](#watcher) running |
| Investigating | A worker finding out more for a question you asked to [enlighten](#enlighten) |

A planned task's card says why it waits: for the tasks it comes after, for the machine, for its wish to be resumed,
for its wish's branch to be green again. A task Djinn resumes says it too, until it runs again. Once the task starts,
the card no longer says it waits, and its page keeps the wait as history: the line that starts the worker says how
long it waited and for what (*started claude … after waiting 8 min 28 s: waits for feat/qol to be green again*), and
the *waiting* lines before it fold into that line, greyed. Only the last *waiting* line of a task that still waits
reads as where it stands. See [Dependencies](#dependencies).

## Where a worker works

Each task in a Git project gets a worktree, a checkout of its own in Djinn's data folder, on a branch named after its
code and title. The worktree starts from the tip of the wish's integration branch, so that the task builds on the work
committed before it. Outside Git there is no worktree: the worker works in the project's folder, and two tasks whose
folders overlap never run together.

A task of a wish without a project runs outside any project: read only, in an empty folder of its own. That suits
research.

## What a worker may do

**Its rights.** What a worker may change comes, in this order, from the wish's rights in the project (**Edit the
files**, or **Edit, in auto mode**), then from the project's own permissions, then from its agent's configuration.
With **The project decides**, the project's rules alone count. A worker in a folder outside Git with no rule reads
only, and asks you whether it may edit: its task shows **Waits for your answer** until you say yes. See
[Rights](#rights).

**What it never does:**

- **Push.** Any push from a worker fails, whatever its agent: Djinn alone pushes, when the wish's push rules say so.
- **Change other tasks.** A worker updates its own task only, and cannot spawn, stop, mark done or cancel work.
- **Answer a question, or make a wish.** It asks; you and the Djinn decide.
- **Write to the Djinn's terminal.**

**What it may do in Djinn:** ask you a question, or revise one it asked; write a [block](#block) for the wish; put a
tilasm; read anything in its wish; and run its heavy commands under the project's [gates](#gates).

**Committing** is Djinn's: a worker leaves its work in its worktree, and when it ends done, Djinn merges it. When a
worker leaves changes not committed, Djinn starts a review worker in the same worktree, which commits what belongs to
the task, leaves the rest where it is, and says which files it left and why.

**What it is told.** Besides its prompt, a worker that edits is told the project's checks, to run them before it ends,
never to make a wish, a question or a task in your Djinn to try something, and to ask you only for a decision or a
move only a person can make.

**Refused calls.** A call its permissions refuse, with no one to approve it, never reaches the window: the Djinn hears
of it when the task ends, with the right that would allow it, and decides. When a new right is needed, it asks you.

## Following a task in the window

The wish's **Tasks** tab shows each task as a row: its status, its code and title, its project, agent and model, how
long it ran or waited, its model turns, its tokens and cost estimate, and, while it runs, its CPU and memory. A click
opens its card: its prompt, the files it writes, its budget, its events, its worker's last word, where its work stands,
and **See logs** for its terminal output.

On the card:

| Button | What it does | When |
| --- | --- | --- |
| **Send** | Gives the running worker an instruction; "Received" shows once it says something after it; a worker that ends before reading it is told it when the Djinn starts it again | A running worker, not a watcher |
| **Pause the worker** | Holds the worker where it is and frees its slot | Running; not on Windows yet |
| **Resume the worker** | Lets a paused worker go on | Paused |
| **Stop the worker** | Ends it; a planned task stops before it starts | Running, paused, planned or resuming |
| **Mark done** | Closes the task, with why in a few words | No worker runs it, and it is not done nor cancelled |
| **Start anyway** | Starts it now, beyond the load bar | Planned, waiting for the machine only |
| **See the diff** | The task's committed work, full size beside the side panel, where you may comment on a line | Any task |
| **See uncommitted files** | What its worktree holds not committed | A task with a worktree |
| **Turn comments into work** | Sends your comments on its diff to be done: the task continues, or a correction task starts | An ended task with comments not sent |

A comment on a line of a running task's diff goes straight to its worker as an instruction.

The diff opens full size beside the side panel; the page under it stays as it was. On its left, the files the task
changes, grouped by folder: each folder counts its files and the lines added and removed under it, and a chain of
folders that each hold one folder reads as one row. Open the folders that matter first. A click on a file, or Enter,
scrolls the diff to it, and the tree follows the diff as you scroll; the arrows move in the tree, Right and Left open
and close a folder. Up to twenty files, every folder starts open; beyond, they start closed. Escape or the close
button brings you back where you were; a click in the side panel closes the diff and goes where it leads.

A worker that holds or waits for a gate cannot be paused: wait, or stop it. Stopping a planned task stops it for good,
and the tasks that come after it fail with it: to drop a task, ask the Djinn to cancel it instead.

## A worker in doubt

A worker decides alone on what is its own: names, internals, what a test settles, what a decision of the wish settles
already. When it meets a real doubt, two valid designs whose consequences you will see, where a wrong pick throws
away the work that follows, it asks you a **blocking question**, and stops:

1. It commits all the work that does not depend on your answer, asks the question, and ends its turn, saying what
   waits for the answer.
2. Its task **waits for your answer**, the question under it. Its slot is free for other work; its worktree, its
   branch, its conversation and the files it writes are kept. The tasks that come after it wait too, their card
   saying "waits for Q51". The question is blocking: it leads **What waits for you**.
3. You answer it. The task goes back in line: it starts again as soon as a slot is free, never before, its branch
   first brought up to date with the wish's.
4. Its worker goes on in its own conversation, told the decision in a line: "Decision Q51: Drop the cache (B), keep it
   small. Go on with your task."

The Djinn hears of each blocking question. It answers one itself only when a decision of the wish or its plan already
settles it, saying so in the note; it may enrich the question for you; a new choice stays yours. A restart of Djinn
loses nothing: the waiting task has no worker to stop.

A question that does not block works otherwise: the worker goes on with its own pick. Answered while it still works,
your decision is passed to it between two of its steps; answered later, it goes to the Djinn, which continues the task
if your answer changes something. A worker asks at most one blocking question at a time and two in all, and three that
do not block: past them, it decides, and says so in its final note.

## Tasks from the Djinn

The window follows tasks; the Djinn makes them. Ask it in words, and it uses these:

- **Spawn** a task, with its title and prompt, its project, its agent and model, its budget, the azima it is part of,
  what it comes after, and the planned tasks it comes before. It may be only planned, to start when it is ready.
- **Continue** a task no worker runs, done, failed, stopped, cut short, or waiting for a blocking question its worker
  asked: a new turn of its own session, in its own worktree and branch, with a new prompt. Refused while it runs (send it a message instead), when its worktree is gone,
  when its session cannot be resumed, or when its wish is granted.
- **Fork** a task: a new task whose worker starts from a copy of that task's conversation, with a session of its own.
  The Djinn may fork its own conversation the same way. Claude and Codex fork; Antigravity cannot. Forking a task that
  failed, stopped or was cut short closes it, continued in the new one.
- **Cancel** a task no longer useful: its worker stops, its worktree goes, its branch stays, and its work leaves the
  merge. The tasks that come after it no longer wait for it.
- **Mark done** a task whose work is done, with why; its unmerged work goes in line to be merged, unless the Djinn
  drops it on purpose, saying why.
- **Update** a task: a planned task may change everything; a running one only its title; an ended one its title,
  agent, model, prompt for its next turn, and azima.
- **Delete** a task no worker of this Djinn ran: a plan item, or one imported from another Djinn.

> **Example.** W9 failed: its tests needed a database. You tell the Djinn "W9 can use the local stack now". The Djinn
> continues W9 on its own session with that instruction, and W9 resumes where it stopped, in its own worktree.

## Budgets

Each task runs under limits: two hours of work in total across its runs, and five hundred model turns in a run, shown
on its row against that limit. Past them it stops, and continuing it gives it five hundred turns again, not more time.
A task may also get a spending cap, which only Claude enforces. A worker silent for fifteen minutes, with nothing
running, is woken and continued on its session; after three wakes in a row, its task fails, saying so. A paused worker
is never silent: its time held still does not count, its silence starts again from zero once it goes on, and it
reads then what was sent to it meanwhile.

A worker whose turn ended with its result, and that says nothing more for ten minutes with its processes idle, is
stopped, whatever keeps it there: a call refused, a wait it sent to the background, a lost stream. If it had written
its final note, its task ends **done**, and its committed work goes to the integration; otherwise it is continued once
on its session, told to finish its task and write its final note, and fails if it falls silent so again. The Djinn
hears it in one line either way. Work it sent to the background that keeps the processor busy, such as tests, keeps
it running.

A worker silent for ten minutes with its processes idle, or a task failed for a reason a new turn may get past, carries
a small circular arrow beside its code: the Djinn relaunches it by continuing it. See
[A worker's health](#restarts-and-updates).

## Urgent tasks

A task waits its turn: the corrections and reviews first, then the tasks Djinn resumes, then the ones others wait
for, then the oldest. A correction or a review never waits for a place: on a full machine Djinn pauses the work task
that comes last to give it its place, and resumes that task once it ends. Two kinds of task may pass that line, marked
**urgent** by the Djinn:

- **A repair**, which unblocks the wish: the repair of an integration branch whose push checks stay red after Djinn's
  corrections, or the task a stuck chain waits for. Its card and its line in the tree carry a small **Urgent** label.
- **Your quick feedback**: a mockup, a prototype or a change you asked to see soon. It is work like any, merged
  through the checks; its label says **Feedback**.

An urgent task:

- goes before every task that waits, whatever their age, a repair before a feedback task;
- takes a place even when the machine is full, as **Start anyway** does, and the load bar counts it among the
  workers started by hand;
- starts while the push checks are red.

One task of each kind may be urgent at a time in a wish: a repair and a feedback task together, never two of the same
kind. A task takes the mark, or loses it, at any time while it is planned, waits, is paused or runs. In the window,
the lightning button on its card makes it urgent for your feedback, and takes off either mark once pressed; when the
wish already has a feedback task marked, a message names it. The Djinn marks repairs. A mark taken off a running
worker keeps its place until it ends. Urgent is never to hurry ordinary work. A study or an
audit that ends in a decision of yours is an [investigation](#investigation) instead: served first too, its code
never merged.

## Good to know

- **Cancel, don't mark done, what is no longer useful.** Mark done is for work that is done; its work is merged.
- **Stop is final for a planned task.** What comes after it fails; cancel drops it cleanly.
- **Send, don't stop, to correct course.** An instruction to a running worker keeps its context.
- **Continue, don't respawn.** A failed task continued keeps its session and its worktree; a new task starts from
  nothing.
- **Look at the waiting reason.** A planned task that does not start always says why.

## Read next

- [Dependencies](#dependencies): what a task waits for.
- [Azima](#azima): the goals tasks are part of.
- [Merges and corrections](#merges-and-corrections): how a task's work lands.
- [Watcher](#watcher): a task with a command instead of an agent.

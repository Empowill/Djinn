# Azima

An azima is a part of a wish's plan: a goal, coded T1, T2 and on, that work is part of. No worker runs an azima
itself; the tasks part of it do the work, and the azima says what they are for, how far they have gone, and when the
part is done.

The word is Arabic, *ʿazīma*: the incantation that binds and commands a djinn. A wish's plan is a graph of azimas.

## Why azimas

A wish of any size takes more than a handful of tasks, and a flat list of thirty tasks says little about where the
wish stands. Azimas give the plan its shape:

- **A goal you can read.** "The PDF rendering", "The export button", "Docs and release notes": each azima says what a
  group of tasks is for.
- **Progress you can trust.** Each azima counts its parts done, running and waiting, and the top of the wish counts the
  azimas done before the tasks.
- **A clear end.** An azima is done when its goal is shown to hold, not when its last task stops: you validate it, or
  its pull request is merged.
- **A branch of its own, if you want one.** Under the per-azima [push strategy](#push-strategy), each azima's work lands
  through its own branch and its own pull request.

The Djinn lays the azimas out when it plans a wish, and adds or regroups them as the plan changes. You rarely make one
yourself; you read them, open the drafts, and validate the finished ones.

## Where you see them

In the wish's **Tasks** tab, laid out as **List: what moves first, then the azimas**, the azimas come below what
moves now, in a single column, each row the full width: its code, its goal, its state, its progress (parts done,
running and waiting), its time and what it waits for. An azima whose work still moves shows its parts open; its
finished parts fold behind a line that counts them, such as **Show the 3 finished**. Click an azima to open its
card: its description, its parts, its branch when it has one, and what validating it takes.

The azimas not done yet show in their **rank**, the first first, whatever their state: the order their work starts
in (see below). The drafts fold behind **Later**, and the done ones at the end, each in their rank too. The order holds
still from one look to the next: an azima moves only when you or the Djinn rank it, never with time or with counts that
change every minute.

## Rank: which azima comes first

Within a wish, the azimas have a rank, as the wishes have one among themselves. **The first azima has priority**: when
the machine is short, the waiting work of the first azimas starts first, and their heavy commands take the
[gates](#gates) first, right after Djinn's own merges and pushes. The wish's own rank still comes first: the work of
the first wish goes before any azima of the second.

- **Change a rank** by dragging an azima by its grip, on the left of its heading, or with Alt+Arrow on the grip. A thin
  line shows where it will land.
- **The Djinn ranks them too**: it puts an azima first, or just before or after another one, when the plan changes.
- **A new azima goes last.** An azima never ranked comes after the ranked ones, by code; the first ranking of a wish
  ranks them all.
- **Work part of no azima** comes after the azimas' work of its wish.

Among the work of one azima, the task that unblocks the most others goes first, then the oldest. A rank orders what
waits; it never makes a task wait: only [dependencies](#dependencies) do.

> **Example.** T04, "The export button", must ship before T02, "The PDF rendering", which the plan made first. You drag
> T04 above T02: as soon as a slot frees, T04's planned task starts, and T02's waits its turn.

## The states of an azima

| State | What it means | What you do |
| --- | --- | --- |
| Draft | Planned for later: a description, no part spawned yet, not counted in the wish's progress. | **Open this azima** when its time comes |
| Open | Ready for work; none of its parts runs nor is done yet. | Nothing: the Djinn spawns its parts |
| In progress | One of its parts runs or is done. | Follow it, answer its questions |
| Watching | A watcher is its only live work, the babysit of its pull request for instance. Not done yet. | Nothing: the watcher wakes the Djinn |
| To validate | Its work is finished, and nothing is left for Djinn: it waits for a proof only a person, a machine, a release or a real model can give. | Check, then **Validate** |
| Done | Validated, its pull request merged, or its plan file says done or has every box ticked. | Nothing |

Apart from its state, an azima whose work is all in main carries a mark, **Merged into main**, told below.

> **Why "To validate" and not "Done"?** Djinn knows when the work of an azima is finished; it cannot know whether its
> goal holds on a real machine, with a real model, after a real release. That proof is yours, or the person's you ask.

An azima closed while some of its work still runs or waits stays **In progress** until that work ends.

## Validating an azima

When every task part of an azima is finished, its card turns **To validate** and says what validating it takes. When
the project keeps its plan in files, that is box by box: each condition of its "Done when" list that still needs a
proof, and who or what gives it: a person, a machine Djinn does not have, a release, or a run on a real model. The
flying carpet gathers them under **Proofs you can give**, and Djinn may ask you, as a move, to check them.

1. Check what is listed: try the feature, run it on the other machine, read the release.
2. Click **Validate**: the azima is done. Answering **Verified** to Djinn's move to check it does the same.

The boxes themselves live in the plan file: the window does not tick them. A box that no longer matters is the
Djinn's to drop from the file, when you tell it so.

> **Example.** T03, "Export invoices as PDF", turns To validate once its four tasks are merged. Its card lists one
> box: "a PDF opens in the system's viewer on macOS". You open one on your Mac, it looks right, you click Validate.
> The wish now counts one more azima done.

## Drafts: planning ahead

A draft azima holds work planned for later, out of the way of what runs now. It has its goal and description, but no
part yet, and it does not count in the wish's progress.

- **Open this azima** turns a draft open, ready for its parts to be spawned, and tells the Djinn.
- **Move**, on a draft's card, sends it to another wish. The Djinn may also move an azima that no worker runs and whose
  parts have not started: its parts go with it, and it may take a new code in its new wish.
- **Rename this azima** changes its title. Its branch, once cut, keeps its first name.

## How work is part of an azima

A task belongs to at most one azima, its **part of**. The Djinn spawns work in an azima, or moves a task into another
one when the plan changes. Finished work part of an azima shows under it in the Tasks tab, with what it spent.

Being part of an azima does not make a task wait. **Only dependencies make a task wait**: a task starts once the tasks
it comes after are done, whatever their azimas. An azima may come after another azima, which says how the goals
follow each other; its work does not wait for that. A task that must wait for an azima comes after the azima itself,
and then waits until the azima is done, validated. See [Dependencies](#dependencies).

## An azima's own branch

Under the per-wish push strategy, every azima's work lands in the wish's integration branch. Under the **per-azima**
strategy, each azima has a branch of its own, named after its code and title (for T07, "The orchestrator", the branch is
djinn/T07-the-orchestrator):

- Djinn cuts it from the project's main branch when the azima is made, or when its first part starts if the azima came
  before the switch. Its card shows the branch, the commit it was cut from, and that it lands in main.
- The azima's work merges into it, checked, and Djinn keeps it up with main as it does a wish's branch.
- It lands in main through its own pull request. Once the branch is pushed and the azima's parts are done and green,
  Djinn asks you, as a move, whether to open it, and opens it as a draft when you say yes. The card shows **PR
  proposed**, **PR open** or **PR merged**, with a link to the request. You merge it on the forge; once it is merged,
  Djinn marks the azima done.
- Djinn pushes the branch by itself under the standard cadence, at the azima's end or after three tasks and an hour;
  on demand, a push button beside the branch pushes it when it is ahead of its remote.
- A project whose branches follow a convention, a ticket's key for instance, can give an azima a branch name of its
  own; a branch that exists already is adopted as it is.

## Merged into main

Once all the work of an azima is in its project's main branch, a small green mark with the merge sign shows beside
its code, in the Tasks tab and in the flying carpet; hover it to read how Djinn found it. Djinn sets it by itself, once
every task part of the azima is finished:

- **Its pull or merge request is merged**, as the watcher that babysits it says, after the last of its work ended. A
  request closed without a merge never sets it.
- **Or every commit of its work is in main**: the azima's own branch and the branches of its done tasks, all in the
  remote's main, which Djinn fetches at the pace it looks at main.

Djinn asks the forge nothing more for it than the watchers and the pull request's card already do. A task added to the
azima clears the mark: its work is not in main yet. Once it is, the mark comes back. The Djinn hears each mark as it
turns on, and when every azima of the wish carries it, it proposes to you to grant the wish.

> **Example.** T04, "The export button", has its pull request merged: its mark shows. A reviewer asks for one more fix;
> the Djinn adds a task to T04, and the mark goes. The fix lands in main through a second pull request, and the mark
> comes back. Every azima of the wish now carries it: the Djinn asks you whether to grant the wish.

Under the per-azima strategy, **no azima waits for another**: each branch holds main and its own work, nothing else.
Djinn refuses to make one azima come after another, and refuses the switch to per azima while azimas depend on each
other, saying which. Tasks inside one azima still wait for each other as usual.

## Azimas from the Djinn

The Djinn makes and keeps the azimas. Ask it in words:

- **Make an azima**, with its goal and description, as a draft for later or open now, after the azimas it follows.
- **Open a draft**, **rename** an azima, or **rewrite its description**, written back to its plan file when it has one.
- **Put a task in an azima**, or take it out: a grouping, never a wait.
- **Rank an azima** first, or just before or after another one.
- **Move an azima** to another wish, with its parts, before they start.
- **Name its branch** after the project's convention, under the per-azima strategy.
- **Close an azima** whose goal holds, as Validate does.
- **Read the plan files** of the wish's projects into its azimas, after you changed them by hand.

What Djinn refuses, and why:

| You ask | Djinn says |
| --- | --- |
| Work in a draft | Open it first: a draft takes no part |
| Validate an azima with parts still running or waiting | It has parts still to finish |
| Cancel an azima | An azima cannot be cancelled; its parts can |
| Change an azima's agent, prompt or budget | No worker runs it: only its title, or its branch, changes |
| Move an azima whose parts have started | Move an azima before its parts start |
| One azima after another, under per azima | No azima waits for another: each branch is cut from main |

## Plans kept in files

A project may keep its plan in its own repository, one file per azima under its plan folder, with a "Done when" list
of boxes. The Djinn reads these files into the wish, and writes back what each azima comes after. A box that says what
it needs, a person, a machine, a release or a real model, is one only that can tick: it is the proof the azima waits for
once its work is done.

## Good to know

- **Azimas are for reading the plan.** You do not have to manage them: the Djinn makes, regroups and fills them.
- **Waiting comes from dependencies, priority from ranks.** If one part must wait for another, that is a dependency;
  if one goal matters more now, rank its azima higher.
- **Validate on evidence.** An azima's state To validate means the work is done, not that the goal holds.
- **Drafts keep the present clear.** Work for later waits as a draft instead of crowding what runs.
- **An azima after another orders goals, not work.** To make a task wait for a goal, it comes after that azima, and
  then waits until the azima is validated.

## Read next

- [Task and worker](#task-and-worker): the work an azima is made of.
- [Dependencies](#dependencies): what makes work wait.
- [Push strategy](#push-strategy): one branch per wish, or one per azima.

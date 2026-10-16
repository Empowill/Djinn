# Azima

An azima is a part of a wish's plan: a goal, coded T01, T02 and on, that work is part of. No worker runs an azima
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
- **A clear end.** An azima is done when you validate it, not when its last task stops: you decide that its goal holds.
- **A branch of its own, if you want one.** Under the per-azima [push strategy](#push-strategy), each azima's work lands
  through its own branch and its own pull request.

The Djinn lays the azimas out when it plans a wish, and adds or regroups them as the plan changes. You rarely make one
yourself; you read them, open the drafts, and validate the finished ones.

## Where you see them

In the wish's **Tasks** tab, below what moves now, the azimas form a single column, each row the full width: its code,
its goal, its state, its progress (parts done, running and waiting), its time and what it waits for, with the work part
of it folded underneath. Click an azima to open its card: its description, its parts, its branch when it has one, and
what validating it takes.

The order holds still from one look to the next, so that an azima stays where you left it:

1. the azimas that move, a worker running;
2. those that wait for you, to validate;
3. the ready ones not started yet;
4. the blocked ones, waiting for others;
5. the drafts;
6. the done ones, folded at the end.

Within each group, azimas follow the plan's order, by code; never an order by time or by counts that change every
minute.

## The states of an azima

| State | What it means | What you do |
| --- | --- | --- |
| Draft | Planned for later: a description, no part spawned yet, not counted in the wish's progress. | **Open this azima** when its time comes |
| Open | Ready for work; none of its parts runs nor is done yet. | Nothing: the Djinn spawns its parts |
| In progress | One of its parts runs or is done. | Follow it, answer its questions |
| To validate | Its work is finished, and nothing is left for Djinn: it waits for a proof only a person, a machine, a release or a real model can give. | Check, then **Validate** |
| Done | Validated, or its plan file says done or has every box ticked. | Nothing |

> **Why "To validate" and not "Done"?** Djinn knows when the work of an azima is finished; it cannot know whether its
> goal holds on a real machine, with a real model, after a real release. That proof is yours, or the person's you ask.

An azima closed while some of its work still runs or waits stays **In progress** until that work ends.

## Validating an azima

When every task part of an azima is finished, its card turns **To validate** and says what validating it takes. When
the project keeps its plan in files, that is box by box: each condition of its "Done when" list that still needs a
proof, and who or what gives it: a person, a machine Djinn does not have, a release, or a run on a real model. The
flying carpet gathers them under **Proofs you can give**.

1. Check what is listed: try the feature, run it on the other machine, read the release.
2. Tick the boxes with their proof, or waive the ones that no longer matter.
3. Click **Validate**: the azima is done.

> **Example.** T03, "Export invoices as PDF", turns To validate once its four tasks are merged. Its card lists one
> box: "a PDF opens in the system's viewer on macOS". You open one on your Mac, it looks right, you click Validate.
> The wish now counts one more azima done.

## Drafts: planning ahead

A draft azima holds work planned for later, out of the way of what runs now. It has its goal and description, but no
part yet, and it does not count in the wish's progress.

- **Open this azima** turns a draft open, ready for its parts to be spawned, and tells the Djinn.
- **Move** sends an azima to another wish: a draft, or an azima that no worker runs and whose parts have not started.
- **Rename this azima** changes its title. Its branch, once cut, keeps its first name.

## How work is part of an azima

A task belongs to at most one azima, its **part of**. The Djinn spawns work in an azima, or moves a task into another
one when the plan changes. Finished work part of an azima shows under it in the Tasks tab, with what it spent.

Being part of an azima does not make a task wait. **Only dependencies make a task wait**: a task starts once the tasks
it comes after are done, whatever their azimas. An azima may come after another azima too, and then its work waits for
that one's. See [Dependencies](#dependencies).

## An azima's own branch

Under the per-wish push strategy, every azima's work lands in the wish's integration branch. Under the **per-azima**
strategy, each azima has a branch of its own, named after its code and title (for T07, "The orchestrator", the branch is
djinn/T07-the-orchestrator):

- Djinn cuts it from the project's main branch when the azima is made, or when its first part starts if the azima came
  before the switch. Its card shows the branch, the commit it was cut from, and that it lands in main.
- The azima's work merges into it, checked, and Djinn keeps it up with main as it does a wish's branch.
- It lands in main through its own pull request: Djinn proposes it, and opens it as a draft when you say yes. The card
  shows **PR proposed**, **PR open** or **PR merged**, with buttons to open or merge it, and **Push** pushes the branch
  when you choose.
- A project whose branches follow a convention, a ticket's key for instance, can give an azima a branch name of its
  own; a branch that exists already is adopted as it is.

Under the per-azima strategy, **no azima waits for another**: each branch holds main and its own work, nothing else.
Djinn refuses to make one azima come after another, and refuses the switch to per azima while azimas depend on each
other, saying which. Tasks inside one azima still wait for each other as usual.

## Plans kept in files

A project may keep its plan in its own repository, one file per azima under its plan folder, with a "Done when" list
of boxes. The Djinn reads these files into the wish, and writes back what each azima comes after. A box that says what
it needs, a person, a machine, a release or a real model, is one only that can tick: it is the proof the azima waits for
once its work is done.

## Good to know

- **Azimas are for reading the plan.** You do not have to manage them: the Djinn makes, regroups and fills them.
- **Order comes from dependencies, not from azimas.** If one part must wait for another, that is a dependency.
- **Validate on evidence.** An azima's state To validate means the work is done, not that the goal holds.
- **Drafts keep the present clear.** Work for later waits as a draft instead of crowding what runs.

## Read next

- [Task and worker](#task-and-worker): the work an azima is made of.
- [Dependencies](#dependencies): what makes work wait.
- [Push strategy](#push-strategy): one branch per wish, or one per azima.

# Wish

A wish is what you want done, said in your own words: "Let customers export their invoices as PDF", "Find out why the
nightly build got slow", "Move the billing service to the new queue". Everything Djinn does belongs to a wish: its
plan, its tasks, its questions, its decisions and the notes written for it.

A wish works on one project or several. It has one lead, the Djinn, who plans it and asks you what it needs to know,
and it ends when you say it is granted.

## What a wish holds

| Part | What it is | Where you see it |
| --- | --- | --- |
| Title and description | What you wished, and the few lines it opens with: what it is for, its scope, where it goes. | The top of the wish |
| Projects | The folders its workers work in, and what they may do in each. | The Wish tab, under Rights |
| Plan | Its [azimas](#azima) and [tasks](#task-and-worker), each after the tasks it needs. | The Tasks tab |
| Questions | What the Djinn needs from you. | The Wish tab, and the bar of what waits for you |
| Decisions | Every question answered, who answered it and the tasks it led to. | The Decisions tab |
| Merged work | Each task's work once merged, to review. | The Review tab |
| Tilasms | The material that explains the wish: pages, diagrams, demonstrations. | The Tilasms tab |
| Blocks | What the Djinn and the workers write down for one another. | The For agents tab |
| Journal | The commands run for the wish, and its log. | The Wish tab, Show the commands |

Djinn keeps all of it, and the Djinn reads where the wish stands each time it turns to it: a wish is its own memory,
and what was decided a week ago is still there, with why.

## Making a wish

Click **New wish** at the top of the side panel. The page asks one question, *What would you like to make happen?*, and
two choices:

- **Projects it works on.** Search them by name or folder. A wish may also be made without a project, for research or
  a plan, and be given one later.
- **Rights in the chosen projects**: **The project decides**, **Edit the files**, or **Edit, in auto mode**. See
  [Rights](#rights).

**Make the wish** creates it and tells the Djinn. You can also ask the Djinn for a wish in its terminal, or with
**Write to the Djinn**: it makes the wish the same way.

> **Example.** You make the wish "Let customers export their invoices as PDF" on the project app, with Edit the files.
> The Djinn opens it, reads the project, and lays out a plan: an azima for the PDF rendering, one for the export button,
> each with a few tasks. The first workers start, and a first question asks you which page size to use.

### A request that belongs elsewhere

When something unrelated comes up while the Djinn works on a wish, it does not slip it into the plan. The window asks
you **Where does this request go?**: file it in an existing wish, keep it where it is, or open a new one. Djinn ranks
the wishes that could take it by their titles, projects and latest notes, without a model.

## Active, paused and granted

Every wish is in one of three states.

| State | What it means | How it gets there |
| --- | --- | --- |
| Active | Its workers run, its questions reach you. Three wishes at most. | Made while fewer than three are active, or resumed |
| Paused | Set aside: its workers are stopped, each on its own session, and its place is free. | Paused by you, or made while three were active |
| Granted | Done for you. Its history stays whole. | Granted by you |

**Three wishes are active at most.** That guards your attention, not the machine: the machine decides how many workers
run. You may have as many paused wishes as you like.

- **Pause**, at the top of a wish, sets it aside. Its workers stop where they are; nothing they did is lost.
- **Resume** makes it active again, and its workers start again where they were. With three wishes active already, it
  takes the last place, and the last active wish is paused.
- **Reopen** brings a granted wish back.

## Rank: which wish comes first

The side panel lists the active wishes by rank, the first first. **The first wish has priority**: when the machine is
short, its workers start first, and its heavy commands take the [gates](#gates) first.

- **Change a rank** by dragging a wish by its grip, on its left, or with Alt+Arrow. A thin line shows where it will land.
- **Make a paused wish active** by dropping it among the active ones: it takes that rank, and if three are active the
  last one is paused. **Make it the first active wish**, beside a paused wish, puts it first at once.
- **The paused and the granted wishes** follow the active ones, each list in its own order, which you change the same
  way. A wish just paused or granted comes first in its list.

Every window shows the same order, and a restart keeps it.

## The principal wish

The star beside a wish's title, **Mark as principal**, makes it the principal wish: the Djinn coordinates from it, and
receives there the answers you give in the other wishes. When none is marked, the active wish with the lowest rank is
principal.

## A wish's page

A wish opens on its **Wish** tab: what waits for you (**Your move**), the questions being investigated, the journal
(**Show the commands**), and the **Rights** of its workers in each project. Beside it:

- **Tasks**: what moves or waits first, then the azimas with the work part of each.
- **Review**: the merged work, a line per task, to mark reviewed.
- **Decisions**: every decision, the latest first.
- **Tilasms**: the material that explains the wish.
- **For agents**, last and discreet: the notes the Djinn and the workers left for one another.

At the top: **Talk to the Djinn**, focused on this wish; **Pause** or **Resume**; **Share**; the wish's
[push strategy](#push-strategy) and the branch its work lands in; and the bin, to delete it.

A wish has a link of its own, which opens Djinn on it from a browser, a chat or a note, and starts Djinn if it does not
run.

## Granting a wish

When every task is finished, none waits on a call its permissions refused that the Djinn has not decided about yet,
and no question is open, Djinn proposes to grant the wish: **My wish is granted**. Only you grant a wish; you may grant
it earlier, and **Reopen** brings it back if something was left.

Djinn never grants a wish by itself, not even one made from a template whose watcher says the work is done: it asks you.

## Deleting a wish

The bin, **Delete this wish**, deletes it for good once you confirm: its tasks and their events, its questions and its
blocks. Its running workers stop first. A worktree with no work of its own goes; one with commits, or with changes not
committed, stays; every branch stays.

To set a wish aside without losing it, pause it; to keep it once done, grant it. Deleting is for a wish made by mistake.

## Sharing a wish

**Share**, at the top of the wish, writes it to one file in your Downloads folder: its plan, questions, decisions,
blocks, tilasms and journal. Never code, which travels through Git, never a secret, never a local path. **Import a
wish**, in the side panel, opens it on another machine. See [Sharing a wish](#sharing).

## Good to know

- **A paused wish loses nothing.** Its workers resume in their own sessions, worktrees and branches.
- **Unrelated work gets its own wish.** It keeps each plan readable, and each decision with the work it concerns.
- **The description is for the Djinn too.** A wish whose scope is written down gets a tighter plan and fewer
  questions.
- **Closing the window pauses nothing.** Active wishes go on while Djinn runs.

## Read next

- [Azima](#azima): how the Djinn splits a wish into parts.
- [Question and move](#question-and-move): what the Djinn asks you, and how to answer.
- [Push strategy](#push-strategy): where a wish's work lands.

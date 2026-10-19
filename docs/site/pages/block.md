# Block

A block is a note the Djinn and the workers write down in a wish for one another: a hand-off, a reference, a report,
a decision, the request the wish started from. Blocks are the wish's shared memory between agents. They are not for
you: what you must see always comes as a [question](#question-and-move).

## Why blocks

Each worker starts in a fresh session, and the Djinn itself may start again from nothing. Whatever one of them learnt
would be lost with its session if it were not written down in the wish:

- **Hand-offs.** A worker that found how the billing module is laid out writes it down; the next worker on billing
  reads it instead of finding out again.
- **References.** A design, a list of endpoints, a diagram of the flow: written once, read by every agent that needs it.
- **Reports.** An investigation's findings, kept whole, while the question that asks you to act on them stays short.
- **Decisions.** What was decided outside a question, written down so that it reaches the [decision log](#decision).

Every new session of the Djinn reads the wish's latest blocks in its brief, and the original request whole.

## What a block holds

| Part | What it is |
| --- | --- |
| Kind | What the block is, in a word: decision, section, report, diagram, or any word the writer chooses |
| Title | One line |
| Content | Markdown, as a rule; another kind of content may be given, an SVG picture for instance |
| About | The task whose worker wrote it, if a worker did |
| Place | Its order among the wish's blocks |
| Icon | One emoji for its subject, shown in the decision log for a decision |

Some kinds go elsewhere than the other blocks:

| Kind | Where it shows |
| --- | --- |
| Decision | The **Decisions** tab, as a decision by the Djinn or by a task |
| Report | **For agents**, and on the card of a question that names it |
| Original request | **For agents**: the request the wish was made from, as you wrote it |
| Request | **For agents**: a request handed over from another wish |

## Reading blocks in the window

Blocks wait in the wish's last tab, **For agents**, discreet, with their count: every block but the decisions. Each
one is folded under its title, with its kind, the task it is about, and when it last changed. A click opens
it:

- Markdown is shown as written, its tables and lists laid out;
- a Mermaid diagram in it is drawn, and so is an SVG picture;
- any other content is shown as it is.

The tab is read only: the window neither edits nor deletes a block, and a block takes no mark. Nothing in it waits for
you, and nothing counts what you have read there.

### A report a question names

When a question names a report of its wish, by its title in quotes or by its id, the question's card shows **Report:**
with the report's title; a click opens it in place, under the question. The report stays in For agents too.

## Who writes blocks

- **The Djinn** writes the wish's blocks as it plans and decides: the shape of the plan, references, decisions you
  told it.
- **A worker** writes blocks in its own wish, each about its own task: the decision log then says it was by that
  task. A worker cannot delete a block.
- **Djinn itself** writes the original request when a wish is made with the full text of a request, and the block
  of a request handed over from another wish.

You do not write blocks. To add something for the agents, tell the Djinn: "Note for the workers: the staging database
is read only." It writes the block, and the next workers read it.

## Blocks from the Djinn

Ask the Djinn in words:

- "Write down how the export pipeline works, for the next workers." It writes a block for the wish.
- "Update the hand-off on billing with what W14 found." Changing a block replaces only what changes.
- "What do the blocks say about the queue?" It lists the wish's blocks, which it can filter by kind or by task.
- "Remove the outdated hand-off." It deletes the block.

When a block concerns you, the Djinn asks you a question that names it: that is how a report reaches you.

## Blocks and tilasms

| | Block | [Tilasm](#tilasm) |
| --- | --- | --- |
| For | The agents | You, and whoever you show it to |
| Holds | A note in Markdown | Pages, diagrams, demonstrations |
| Where | **For agents** | **Tilasms** |
| History | Replaced when changed | Each version kept, earlier ones restorable |

## Blocks outside the window

- **The wish's brief**, which every session of the Djinn starts from, holds its original request whole and its latest
  blocks, cut short when long.
- **Sharing a wish** carries its blocks to the other machine. See [Sharing a wish](#sharing).
- **The wish's page**, rendered to one file, shows them as notes, and decisions under Decisions.

## Good to know

- **Blocks are not a channel to you.** A worker that needs you asks a question; a block waits unread by any person.
- **Write for the next agent.** A good block says what was found and where, so that the next worker does not search
  again.
- **One subject per block.** Short blocks with clear titles read better in the brief, where long ones are cut.
- **A decision is a block of kind decision.** Written that way, it joins the decision log; any other kind stays in For
  agents.

## Read next

- [Decision](#decision): where decision blocks show.
- [Tilasm](#tilasm): material that explains a wish to you.
- [Question and move](#question-and-move): how a report reaches you.

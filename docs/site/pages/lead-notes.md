# Lead notes

Lead notes are how to lead wishes with Djinn, learnt from past wishes: what a developer expects of a lead, and why.
One note per subject, a few lines each, built into Djinn itself and read by the Djinn whatever its agent, Claude
Code, Codex or Antigravity.

They are the Djinn's experience, kept where every lead finds it. A lesson learnt once, after a stalled wish or a
question that made no sense, becomes a note, and the next lead starts with it.

## Why they exist

An agent session forgets. A new session of the Djinn, or a Djinn on another agent, would make again the mistakes a
past one was corrected for: a blocker explained only in its terminal, which you never read; a question asked twice in
two forms; a context so long that the decision drowns in it. Writing these lessons in the agent's own memory would
keep them for one agent on one machine.

Lead notes keep them in Djinn instead. Every Djinn reads the same notes, on every machine and on every agent, and they
change only when Djinn changes: they are part of the lamp's release, not of one session's smoke.

## What a note holds

Each note has:

| Part | What it is |
| --- | --- |
| Name | A few words joined by hyphens, which the Djinn uses to open it. |
| Description | One line on what it is about, for a lead to tell whether to open it. |
| Kind | Feedback (how the developer wants to be led), project (a fact of the work) or reference (where to look). |
| Text | The rule or the fact, then **Why**, the incident or the developer's words that led to it, then **How to apply**. |

A note may link to another one, and most do: the notes on questions point to one another.

## How the Djinn reads them

The Djinn does not read every note each time. Its brief, what it reads at the start of every session, holds the
index: a line per note, its name and its description, never the notes themselves. Before it acts in a note's area,
asking a question, planning the order of tasks, deciding whether to ask you for a push, it opens that note, or several
at once.

So a note costs the Djinn a line until it is needed, and its whole text only when it is.

## The notes Djinn carries

The notes change with each release of Djinn. Today they cover:

- **Talking to you.** Everything for you is a question in its wish, a report or a "done as you asked" included; never
  ask what is obvious or already decided; a question reads at a glance, and its details go in a [tilasm](#tilasm).
- **Marking what waits.** A question is marked as your move, as needed before something, or as one that can wait, and
  never inflated to be seen nor left unmarked when it really blocks. See [Question and move](#question-and-move).
- **Planning.** Keep the plan's order free of conflicts while as many workers as possible run side by side; plan the
  task of each option when asking, so that your one click launches it.
- **Pushing.** When Djinn pushes by itself, and why the Djinn then never asks you to push.
- **Its own commands.** How the Djinn runs Djinn's commands so that none needs your approval in its terminal.
- **Work that is not done.** A worker that ended its turn while a long run went on lost that run's result.

## How a note is added

Nothing writes a note while Djinn runs: the notes are read only. A note is added or fixed like any part of Djinn's
documentation, by a change to Djinn's own repository, reviewed and released. A Djinn that works on Djinn itself does it
by spawning a task that edits the notes.

For a rule of your own project, how its workers test, which agent it uses, what its branches are for, a lead note is
not the place, and no folder of your project holds lead notes. Write it in the project's own instructions, its
AGENTS.md, CLAUDE.md or CONTRIBUTING.md: the Djinn's brief names those it finds and tells it to read them first. See
[Project and settings](#project-and-settings).

## What to expect

- **You do not see the notes in the window.** You see them at work: a question that fits on one screen, marked for
  what it really holds up; a report that comes as a question, not as a line in the terminal.
- **Every agent leads the same way.** Switching the Djinn from Claude Code to Codex keeps the notes; they are Djinn's,
  not the agent's.
- **A new release can bring new notes.** The next session of the Djinn reads the new index.

## Common mistakes

- **Correcting the Djinn in its terminal and expecting it to stick.** A word to the Djinn holds for its session. A
  rule meant to last belongs in your project's own instructions, or, for how any lead should work, in a note of Djinn.
- **Confusing notes with blocks.** A [block](#block) is what the Djinn and the workers write for one wish. A note is
  for every wish, and no wish writes it.
- **Putting a project's pitfalls in a note.** A note is about leading, whatever the project. The traps of a
  codebase are for the workers who work in it, in that project.

## Read next

- [The Djinn](#the-djinn): who reads the notes, and what it decides alone.
- [Question and move](#question-and-move): the rules most notes are about.
- [Skills and templates](#skills-and-templates): what a project teaches its workers.

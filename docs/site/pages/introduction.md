# Introduction

Djinn turns what you wish into work done by coding agents, on your own machine, with your own accounts. You say
what you wish, in a sentence. The Djinn, its lead, splits it into tasks. Workers carry them out, each in a place of its
own. You answer the questions that only you can answer, and you grant the wish when it is done for you.

Djinn is one app: a window, a local server behind it, and a command line that the agents use to drive the work. You
never need that command line yourself: everything you do in Djinn, you do in the window, with words and clicks.

## Why Djinn exists

One coding agent in one terminal is easy to follow. Three of them on three pieces of work is not: which one is doing
what, which branch holds whose work, who decided what and why, whether the tests of one broke the build of another,
whether the machine can take a fourth. Most of that bookkeeping falls on you, and none of it is the work you wanted
done.

Djinn takes the bookkeeping. It keeps the plan of each wish, starts the workers when the machine has room for them,
commits each one's work once it is checked, pushes on a cadence, and brings you only what needs you: a question, with
the options and the one the Djinn recommends. **You stay in control of the decisions; Djinn holds everything else.**

## The lamp and the smoke

A djinn is smoke from a lamp: you look for the lamp, you find the djinn. Djinn is built the same way, a hard lamp and
free smoke, and knowing which is which tells you what you can rely on.

- **The lamp is hard.** What Djinn computes and must never get wrong: wishes, tasks and their states, questions,
  decisions, gates, the machine, the journal, the limits. Code computes it and tests check it; no model decides it.
  When the window says a task is done, a question is blocking or a check is red, that is a fact.
- **The smoke is free.** What the Djinn shapes for each wish as it goes: notes it writes for the workers, a diagram, a
  report, a page that explains the wish. Blocks the model writes, shown as written, exported whole. Read it as you
  would read a colleague's notes.

Three rules follow from it, and you will see them everywhere in the window:

- **Empty means hidden.** No "To decide" section without a question, no list without an item.
- **The model chooses the smoke.** When the Djinn needs a new kind of note, it writes one; nobody changes Djinn for it.
- **Smoke hardens only when code needs it.** Then it becomes part of the lamp, with its tests.

## What no djinn can grant

Like the genie of the lamp, Djinn has limits. They are its first rule, code enforces them, and no option turns them
off.

- **It can't kill anybody.** Nothing destructive or irreversible, a deletion, a force push, a deployment, happens
  without your explicit go.
- **It can't make anybody fall in love.** It never bends anyone else's will: no message, approval or merge sent in your
  name that you have not seen.
- **It can't bring anyone back from the dead.** What failed stays failed: no made-up result, every claim comes with
  its evidence.
- **No wishing for more wishes.** It never grants itself more than it was given: no extra budget, no wider access, no
  secret it stores or reads.
- **Three wishes at a time.** Three wishes are active at most, and as many set aside as you like. It guards your
  attention, not the machine.

> **Workers never commit, and never push.** They edit the place they work in; Djinn commits each one's work as it
> ends, checked, and pushes on a cadence.

## Who it is for

Djinn is for people who give work to coding agents and want several pieces of it going at once without losing the
thread:

- **A developer** who already works with Claude Code, Codex or Antigravity, and wants them to work in parallel on one
  project or several, each change checked before it lands.
- **A lead of a project** who wants to see at a glance what each agent does, what it costs, and what waits for a
  decision, and to keep every decision with the work it led to.
- **A team** that shares how its agents work: the checks to run, the agent and model to use, the skills to follow,
  kept in its repository so that every Djinn on every machine works the same way.

You do not need to know Djinn's command line, nor how its agents are driven. You need an agent installed and signed in
on your machine, and a folder to work in. Djinn asks for no key: the account each agent signs in with pays for it.

## The words you will meet

Two words come from the theme: you make a *wish*, you *summon* a skill. A few more are borrowed from the old tales,
each where nothing plainer would do. Everything else is plain.

| Word | What it is |
| --- | --- |
| [Wish](#wish) | What you want done. It has its projects, its plan, its questions and its decisions. |
| [The Djinn](#the-djinn) | The lead: one agent session that plans every wish, asks the questions and keeps the plans true. |
| [Azima](#azima) | A part of a wish's plan, coded T01, T02 and on: a goal that work is part of. |
| [Task and worker](#task-and-worker) | A piece of work, coded W1, W2 and on, and the agent that does it, in its own worktree. |
| [Question and move](#question-and-move) | What the Djinn needs from you: a decision, or a move only you can make. |
| [Decision](#decision) | An answered question, kept with who took it and the tasks it led to. |
| [Block](#block) | What the Djinn and the workers write down for one another. |
| [Tilasm](#tilasm) | The material that explains a wish: a page, a diagram, a demonstration. |
| [Gates](#gates) | How heavy commands share the machine, one at a time. |

## Where to go next

- [Getting started](#getting-started) takes you from the install to your first wish granted, step by step.
- [Wish](#wish), [Azima](#azima) and [Question and move](#question-and-move) explain the three things you will handle
  every day.
- The [Command line](#commands) lists every command the agents use, should you want to see what they can do.

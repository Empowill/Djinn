# Enlighten me

**Enlighten me**, beside **Rub the lamp** on a question's card, asks for more before you decide. Your note says what
to look into: "what would B cost on the mobile app?", "is the old export still used?". A small worker, the
investigator, digs into it, then revises the question, and the question waits for your answer again.

## Why it exists

A question is short on purpose: it reads at a glance. Sometimes that is not enough. The options leave out the one you
had in mind, a fact the recommendation rests on looks doubtful, or you simply do not know enough yet. Guessing would
make a decision you might undo; answering with a question in the note would have the Djinn turn it into work.

Enlighten me keeps the question open and sends someone to find out. You decide once you know, and the question you
answer is a better one: its facts checked, its options revised, its recommendation updated.

## How to use it

1. Open the question, and type in its note what you want to know. An empty note asks for a look in general.
2. Click **Enlighten me**, or press Ctrl+Shift+Enter (Cmd+Shift+Enter on macOS) from anywhere on the card. The window
   says the Djinn will investigate before you decide.
3. Go on with other questions. This one waits apart, under **Being investigated**.
4. When it comes back, revised, it opens again among the questions that wait for you. Read it, then answer, or ask
   for more light again.

## What to expect

### While it is investigated

The question leaves the questions column for the **Being investigated** section, below it: one line with its code,
its text, how long it has been investigated, and **You asked:** with your note. It is not in **What waits for you**:
nothing waits on you there.

A click opens it. You may still decide now: **Rub the lamp**, **Withdraw question…** and **Move to another wish** stay
on the card, and your answer settles the question.

Asking for more light again while the investigator still works sends your new note to that same investigator: one
investigates a question at a time.

### Who investigates

By default, a small worker of its own, the investigator, started for that one question. It works in the project's
folder, and it only reads:

- it reads the project's files and its history, and searches the web;
- it reads the wish: its brief, its tasks, its questions, its notes and its [tilasms](#tilasm);
- it edits none of the project's files, takes no [gate](#gates) and has no worktree. Under Claude Code it writes one
  file only, the new context of its question, in a folder of its own that Djinn keeps for it: Claude Code refuses a
  context of several lines passed inline. An investigator set on an agent that cannot only read, Antigravity today,
  runs on Claude Code instead, with that folder too.

It may still leave a note for the agents, and spawn a worker for what needs writing, a tilasm that lays out a long
analysis for instance; the question links that tilasm rather than carrying the analysis.

A project may turn investigators off, in its settings. Then the Djinn investigates itself, and it does so too when an
investigator does not start within 20 seconds. Either way the window says the same: the Djinn looks into it.

### The revised question

The investigator always revises the question once at the end, even when it found little, so that the question never
hangs. A revision may change its context, its options, its recommendation, and how much it holds up; what it does not
change stays.

The revised question keeps its place in the column and opens again. Its card says **Revised**, or **Revised ×2** after
two rounds, with when. Its context is its current version only: **Versions**, a small button in the card's corner,
opens the earlier versions below it to read, the last first, each with what you asked to investigate about it. A
second click closes them.

### What it costs

An investigator is an agent, and it runs on your agent's account:

- **Its model.** A lighter model by default, Sonnet on Claude Code. The project's settings may name another.
- **Its budget.** 2 US dollars at most by default, where its agent can enforce a budget. The project's settings may
  change it, or remove the limit.
- **A place on the machine.** It takes a worker's place like any agent. When the machine is full, the newest work task
  pauses to make room, and resumes after.

### When it fails

An investigator stopped by a refused command is tried once more by a new one. Any other failure is the Djinn's: the
question stays **Being investigated**, and the Djinn is told that it still waits for a revision. A failed investigator
never waits for you, nor notifies you. One whose question was answered, withdrawn or revised meanwhile closes by itself,
and an investigator that finished with refused tool calls is acknowledged when its question is answered or withdrawn so
it never holds back granting the wish.

## Common mistakes

- **Answering with a question.** "What about the mobile app?" as the note of an answer is an answer: the Djinn turns it
  into work. Ask it with Enlighten me.
- **An empty note on a precise doubt.** The investigator digs in general; a sentence on what you doubt gets you a
  sharper revision.
- **Waiting for it when you already know.** You may decide at any time, while it is investigated.

## Read next

- [Question and move](#question-and-move): the card, its levels and its keys.
- [Tilasm](#tilasm): where a long analysis goes.
- [The Djinn](#the-djinn): who investigates when no investigator does.

# The Djinn

The app is Djinn; its lead is the Djinn. It is one agent session, on Claude Code, Codex or Antigravity, that leads
every active wish at once: it reads where each wish stands, splits the work into [azimas](#azima) and
[tasks](#task-and-worker), sends the workers, turns your answers into work, and asks you what only you can give. The
workers are its hands; the Djinn is the one voice you talk with.

## Why one voice

With an agent per wish, you would follow three conversations, each with its own memory of what you said, and repeat
yourself in each. One lead for every wish sees them all together: it knows that a task of one waits on a decision of
another, that two wishes touch the same files, that you already answered the same question elsewhere.

The Djinn also keeps no memory of its own that matters. Djinn computes where every wish stands, its plan, its
questions, its decisions, and hands it to the Djinn at the start of each session. A new session, or the Djinn on
another agent, picks up exactly where the last one left.

## What the Djinn reads first

Every new session of the Djinn starts from its brief, which Djinn writes from its own records, no model involved:

- **Every active wish**: its title and description, its projects, where its work lands, its azimas, what runs and
  what waits, its open questions with how much each holds up, and what is being investigated.
- **The paused and granted wishes**, a line each.
- **Djinn's rules for leading**: how to ask, how to plan, what it may do alone.
- **The index of the [lead notes](#lead-notes)**: a line per lesson learnt, which it opens when it acts in that area.
- **The projects' own rules**: the instructions each project holds, which it reads before it works there.

When it turns to one wish, the Djinn reads that wish's own brief: its finished work, its latest decisions, what you
marked read, its latest blocks and its tilasms.

## Talking with the Djinn

The Djinn talks to you only in the window, and only by [questions](#question-and-move). A report, a doubt, a choice, a
"done as you asked" all come as a question in the wish they concern, often a yes or no with "OK, nothing to do" as an
option. You never need to read its terminal: it writes one short line there at most, never a question, and a line you
type there is answered in the window.

You have three ways to reach it:

- **Answer its questions.** This is the usual way: an answer goes to the Djinn, which turns it into tasks.
- **Write to the Djinn**, the pill at the bottom of a wish, or of the flying carpet. Ctrl+I (Cmd+I on macOS) opens it,
  Enter sends, Shift+Enter breaks the line, Escape folds it and keeps your draft. From a wish, your words reach the
  Djinn with the wish named; they wait while you type in its terminal or a choice is on screen there, then go.
- **Talk to the Djinn**, at the top of a wish, opens its terminal in the window, focused on that wish. At the top of
  the flying carpet it opens it on no wish. There you talk to it as to any agent.

### Starting it, and its agent

At every start, Djinn brings the Djinn back on its session, in its terminal. **Talk to the Djinn** does the same if it
does not run, or shows it if it does.

- **No session yet.** A new Djinn starts on Claude Code, from its brief, in the first of your projects with a folder on
  this machine. Never in your home folder: with no project at all, nothing starts, and Djinn asks you to create one.
- **Choose the agent**, the arrow beside **Talk to the Djinn**, lists the agents found on this machine. One that is not
  installed is greyed, saying so; the current one is checked, and picking it resumes it. Picking another starts the
  Djinn anew on that agent, from the brief. **Set up the agents**, at the bottom, opens their setup.
- **While the Djinn runs**, another agent starts nothing: Djinn says to quit it in its terminal first.

A small dot on **Talk to the Djinn** says the Djinn could not start, or that its program ended: its tooltip gives the
error or the exit code, and its tab keeps its last output. **Talk to the Djinn** tries again.

### When its agent asks for approval

The Djinn's agent may stop to ask for your approval in its terminal: a command to run, a folder to trust. Djinn
reads that choice from the screen and puts it first in **What waits for you**, on the flying carpet and in every
wish, as **Approval for the Djinn**, with the agent's options as buttons; a notification says so too. A button
answers in the terminal as its keys would, and only while that very choice is still on screen. It comes first because
it holds the Djinn up, and so every wish.

## Djinn drives the lead

By default the Djinn lives in a terminal: one long session that waits for what it must act on. With the lead
driven setting on, in the settings file of Djinn's data folder, read when Djinn starts, Djinn drives it instead
(decision Q31), and no terminal runs it:

- **A batch, a fresh session.** When events wait for the lead (a task ended or failed, a merge, your answer, a move,
  a restart), Djinn lets them settle two seconds, then starts a new headless Claude Code session for that batch. It
  acts with Djinn's commands, ends its turn, and Djinn lets it go. One batch runs at a time; what comes meanwhile
  goes to the next one.
- **Everything in its prompt.** The short rules of the lead, versioned, in its system prompt; then the events whole,
  the brief of each wish they are about, the active wishes, and the lead's journal. No session remembers anything:
  the next one reads what the last ones did.
- **The lead's journal.** Each batch is kept: who led it (agent, model, session), the events it took, the commands it
  ran, the line that ended its turn, and what it cost. The command line prints it, for every wish or for one.
- **Write to the Djinn** sends your words to the next batch, whole. Nothing reaches a terminal: what the Djinn has to
  tell you is a question.
- **A failed batch** gets one new session. Failed again, it shows beside **Lead** in the window and its events wait:
  the next event, or a click on **Lead**, starts a session for them.

- **Where it runs.** The Djinn runs on a seat you choose: an agent (Claude Code or Codex), a model, an account. The
  arrow beside **Talk to the Djinn** lists each agent on its own sign-in, then on each of its accounts, and the model:
  one click moves the Djinn there, and the command line does the same. The next batch starts there with the lead's
  journal, and the Djinn stays there until you move it again. Antigravity cannot lead yet.
- **A limit is your call.** When the Djinn hits its subscription's limit, Djinn does not move it by itself: the
  limit shows first in the attention bar, with a button for each agent or account signed in on this machine. One
  click sends the waiting events there. Without a click, they wait for the limit to reset.
- **Several subscriptions on one machine.** An account is a folder of the agent's own, beside its default one, where
  it keeps its login: Claude Code's configuration folder, Codex's home. Name one in the agents' setup or with the
  command line, sign in there once with the agent's own command, in the panel's terminal; Djinn keeps no token, only
  the folder. Antigravity has no such folder.

A batch costs about $0.16 to $0.22 on Opus 5.5, as Claude Code counts it, measured on 16/10/2026: about 18,600 tokens
of rules, tools and system prompt read from the cache the last session wrote, and the batch's own message written to
it.

### Its tone follows the theme

The driven Djinn speaks in the tone of the theme the window shows (decision Q36): the solar Djinn in the light theme,
warm, the good news first, with puns of light and lamps; the mocking demon of the lamp in the dark theme, dry and a
little ironic, its recommendations offered as pacts with no fine print.

- **What the window shows.** The window tells Djinn the theme it shows, the system's resolved: with the system's
  theme, the Djinn turns solar when your desktop turns light. The last window to say it wins, and Djinn keeps it
  across restarts. Until a window says it, the Djinn stays plain.
- **Read as each batch starts.** A batch speaks in the tone of the theme shown as it starts; a question already asked
  keeps the tone it was written in.
- **Only the tone changes.** The humour lives in a question's title, the end of its recommendation and one short
  aside; never in the facts, the numbers, the commands, the options, a right, an incident or bad news, nor in what
  leaves Djinn: a commit, a pull request, a message for a colleague or a customer. Workers stay plain.
- **Yours to edit.** Each tone is one line of its own file, in the lead folder of Djinn's repository, beside the lead
  notes: the solar one in the light file, the demon in the dark one.
- **What it costs.** Measured on Opus 5.5 on 17/10/2026: the tone in a batch's message is 127 tokens in light, 145 in
  dark, written to the cache once a batch; the rule that bounds both, 283 tokens of the rules, is read from the
  cache. Together, less than $0.002 a batch, about 1 % of one.

## What it decides alone

The Djinn runs the work without asking you for what an agent can do:

- **Planning.** It splits a wish into azimas and tasks, orders them so that workers do not collide, and keeps as many
  running side by side as the plan allows.
- **Checking.** A check, a measure, a log to read, the result of a CI run: it sends a worker or a watcher, and reports
  what they found. It never asks you to look.
- **Running the work.** Failed merges, held pushes, conflicts and corrections are its to sort out. A worker's refused
  calls are its too: you see them only if it needs a right only you can grant.
- **Damage control.** It may stop workers, withdraw a question or undo a faulty merge on its own, and tells you at
  once.
- **Your answers.** It turns each answer into tasks itself, in one pass. When it planned the task an option launches,
  your answer without a note launches it at once.

It does not read or change code itself: all the work goes to tasks.

## What it asks

- **A decision** that is yours: a choice of design, of scope, of order, with its recommendation.
- **A move** only a person can make: push a tag, open or merge a pull request, start a release, check on a machine
  Djinn does not have, grant a right, install and restart. Its question gives the exact commands, in order.
- **A right** its workers lack, asked narrowly, never broad by default.
- **A doubt about your answer.** When it does not fully understand an answer, it asks again before any work starts.
- **Granting a wish**, which is never its call: it proposes, you grant. See [Wish](#wish).

## A request that belongs elsewhere

When you ask the Djinn for something unrelated to the wish at hand, it does not slip it into that plan. It hands the
request to Djinn, and the window asks you **Where does this request go?**: file it in an existing wish, keep it where
it is, or open a new one, each option with its reason. Djinn ranks the wishes that could take it by their titles,
projects and latest notes, without a model. A request that matches a project's wish template makes a wish from that
template. See [Skills and templates](#skills-and-templates).

## What to expect

- **Silence in its terminal is normal.** The Djinn ends its turns without a word there; what it has for you is in the
  window.
- **Your answers are never lost.** Each event for the Djinn waits until it has taken it. An answer given while the
  Djinn does not run says **Waiting for the Djinn**, and reaches it once it starts.
- **It waits for events, not for you.** Between turns, the Djinn waits for what it must act on: an answer, a task
  that ends, a watcher that speaks.
- **It reads each event whole.** Task and question codes count within a wish, so two wishes can each have a W74:
  every event names its wish beside its code. A task's final note and the note of your answer come whole with their
  event, a very long note up to a screen or two, with the command that prints the rest. To read one question and its
  answer, or the last words of a task that still runs, the Djinn has a command that prints only them, at once.

## Common mistakes

- **Talking to it in its terminal and waiting for an answer there.** It answers in the window. Its terminal is for
  when you want to see it work, or speak to it at length.
- **Asking it to do several unrelated things in one wish.** Each gets its own wish, through **Where does this request
  go?**: plans stay readable, and each decision stays with its work.
- **Expecting it to remember a correction.** A word in its session lasts for that session. A rule meant to last
  belongs in your project's instructions; how any lead should work belongs in a [lead note](#lead-notes).
- **Switching agents while it runs.** Picking another agent while the Djinn runs starts nothing; quit it in its
  terminal first, then pick the other agent.

## Read next

- [Question and move](#question-and-move): what the Djinn asks you, and how to answer.
- [Lead notes](#lead-notes): what the Djinn has learnt from past wishes.
- [Restarts and updates](#restarts-and-updates): how the Djinn comes back after a restart.
- [Usage and cost](#usage-and-cost): what the Djinn and its workers cost.

# The Djinn

Djinn has one lead for every active wish: the Djinn. It is an agent session, Claude Code, Codex or Antigravity, that
talks with you, splits wishes into tasks, asks the questions and keeps the plans true. **Talk to the Djinn**, at the
top of a wish, opens it in the window's terminal, focused on that wish; **Write to the Djinn** sends it a word without
leaving the page.

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

A batch costs about $0.16 to $0.22 on Opus 5.5, as Claude Code counts it, measured on 16/10/2026: about 18,600 tokens
of rules, tools and system prompt read from the cache the last session wrote, and the batch's own message written to
it.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Question and move](#question-and-move), [Lead notes](#lead-notes), [Usage and cost](#usage-and-cost).

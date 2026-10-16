# Question and move

A question is how the Djinn, or a worker, asks you for what only you can give: a decision, an answer, or a move. The
Djinn asks rather than guesses, and it talks to you only this way: a report, a doubt, a "done as you asked" all come as
a question in the wish they concern.

Each question comes with what is at stake, the options, and the one the Djinn recommends and why. Once you answer it,
it is a [decision](#decision): kept with who took it, and the tasks it led to.

## Why everything is a question

You do not read the Djinn's terminal, nor the workers', and you should not have to. A question is the one place where
the work meets you: it names its wish, it says how much it holds up, it carries the Djinn's advice, and your answer is
kept. Nothing waits on a line you never saw.

Two things follow:

- **An answered question is resolved.** The Djinn does not bring it back; it asks a new one only on a real doubt.
- **A question goes as soon as it is moot.** When a task, or Djinn, does what a question asked, the Djinn withdraws it.

## Reading a question

A question is a card, coded Q01, Q02 and on in its wish, its title opened by an emoji for its subject. From top to
bottom:

1. **The question**, in a sentence or two.
2. **What is at stake**: for a move, first the exact commands to run, in order; then a line on why, a few facts, what
   each option changes and costs. It fits on one screen; the details of a hard choice are in a [tilasm](#tilasm) it
   links, which opens over the page.
3. **Report:** with a report's title, when the question names one of the wish's reports: a click opens it in place.
4. **The recommendation**: the option the Djinn recommends, and why. Read it first; it is often enough.
5. **The options**, A to D, four at most, the recommended one marked **Recommended**. A question may have none: then it
   asks for a yes, or for your answer in words.
6. **The note** and the gestures to answer.

A card outside the questions column, among those being investigated for instance, folds a long context under **Read
more**.

## How much it holds up

Djinn computes how much each question holds up, from what the Djinn marked and from what waits on it:

| Level | When | What the card shows |
| --- | --- | --- |
| Blocking | A worker waits on the answer before it may edit. Djinn sees it wait. | **Blocks W12**, in red |
| Your move | A move only you can make, needed before a step the Djinn names. | Its words, "before the merge", in gold |
| To decide | An answer needed before something, in the Djinn's words. | Its words, in gold |
| Can wait | Everything else: useful, not urgent. A move with no step named can wait too. | **Can wait**, in grey |

The Djinn sets these marks, and its rules ask it to mark each question right: never inflated to be seen, never a real
blocker left unmarked. When a question's urgency changes, it revises it.

### What waits for you

**What waits for you**, the bar at the top of each wish and of the flying carpet, says how many things wait, and lists
only what keeps a wish from moving forward, the most blocking first:

1. the Djinn's own agent asking for an approval in its terminal, which holds every wish up (see
   [The Djinn](#the-djinn));
2. a lit [ifrit](#ifrit);
3. the questions that are blocking, your move, or to decide, in that order;
4. the answers that wait for the Djinn to start;
5. the workers waiting for your answer before they may edit;
6. the wishes ready to grant.

A click takes you to the line's card. A question that can wait is not in the bar: it is in the questions column alone.

## The questions column

The questions column holds every open question waiting for you, those that can wait included, as they were asked:
the oldest first, each card with its own level. On the flying carpet it groups them by wish, each under its wish's
title; in a wish, it holds that wish's own. Below it come **Your move**, the workers that wait for you and the wish
ready to grant, then the questions **Being investigated**.

### The focused question

One question of the column is open at a time, the first at the start; the one you work on carries a gold ring. When it
leaves, answered, withdrawn or moved, the one below opens, else the one above.

| Keys | What they do |
| --- | --- |
| Up, Down | The previous or the next question, across wishes; it opens, its title at the top of the page |
| Home, End | The first or the last question |
| A to D, or 1 to 4 | Pick an option (not while typing in the note) |
| Enter on the question | Put the caret in the note |
| Enter in the note | Rub the lamp: answer with the option selected |
| Shift+Enter | A new line in the note |
| Ctrl+Enter (Cmd+Enter on macOS) | Answer, from anywhere on the card |
| Ctrl+Shift+Enter (Cmd+Shift+Enter on macOS) | Enlighten me, from anywhere on the card |
| Ctrl+Shift+Backspace (Cmd+Shift+Delete on macOS) | Withdraw the question, once you confirm |
| Space, Page Down | Scroll a long question |
| Escape | One step back: from the note to the question, then the pick undone, then out of the column |

With nothing focused, the first Up or Down, anywhere but in a field, focuses the open question. Enter or Space on an
option only picks it, and an input method still composing answers nothing. The keys stay on the card: they never take a
key from the terminal or another field. On a card outside the column, the arrows pick the options.

## Answering

Under the options, one field takes your note, **Note with the answer**, with three gestures:

- **Rub the lamp** answers with the option selected and your note. The recommended option is selected from the start,
  so one click takes the Djinn's advice; pick another option and the lamp sends that one. With options and none
  recommended, pick one first. A question with no options takes your words as the answer.
- **Enlighten me** asks for more before you decide. See [Enlighten me](#enlighten).
- **Withdraw question…** closes a question that no longer matters, without an answer, once you confirm, with a note on
  why if you like. It shows as **Withdrawn**, and is no decision.

### Where your answer goes

Your answer goes to the Djinn, which turns it into tasks itself, placing them in the plan. Three answers act without
it:

- **A planned option.** When the Djinn planned the task an option launches, picking it without a note launches that
  task at once, if it waits on no other task and holds none back. A note, or such a link, goes to the Djinn first,
  which adjusts the task to it.
- **A worker's request to edit.** Your yes lets the waiting worker go on.
- **Djinn's own moves**, below.

If the Djinn is not running, the answer is kept and says **Waiting for the Djinn**: it gets it once it starts.

> **Example.** Q07 asks: "The export takes 40 s on large invoices. Render in the background?" The recommendation is A,
> "Yes, with a notice when the PDF is ready". You agree, type "keep it under 2 s for small ones" in the note, and rub
> the lamp. The Djinn spawns the tasks, and the decision log shows Q07 with your note and the tasks it led to.

## Later

**Later**, the clock in the top right corner of the open card, puts a question off: it goes to the bottom of the list,
below every question asked before, and stays there, in the window and for the Djinn. A question asked afterwards still
comes below it. The card says when you put it off, and the next question opens.

Later changes the order only, never the level: a blocking question put off still blocks, and still leads the bar. The
same button, pressed again, gives the question back its place. A question being investigated has no Later.

## Moving a question

**Move to another wish**, in the card's corner, moves a question that belongs elsewhere. Choose the wish; **Tasks
following this decision follow it** brings along the tasks that will follow its decision. The question takes the next
code in its new wish.

## Moves

A move is a question about something only you can do, through the window or outside Djinn: push a tag, open or merge a
pull request, start a release, check on a Mac or a Windows machine Djinn does not have, grant a right, install and
restart. Its context starts with the exact commands to run, in order, each with what it prints and what to expect. When
it names a page, a pull request or a CI run, the card and its line in the bar carry a button to it, **Open PR #12** for
instance.

Some moves are Djinn's own, asked when Djinn itself needs your go. A yes, their first option, then acts on its own:

| Move | What your yes does |
| --- | --- |
| Install and restart a build | Installs that build; the update banner then offers to restart |
| Push a tag | Pushes the tag |
| Open a pull request | Opens it as a draft, following the project's own way of opening one when it has a skill for it |
| Check an azima on a machine | Marks the [azima](#azima) done, once you answer **Verified** |

Any other move, asked by the Djinn or a worker, is noted: you make it, and the Djinn reads your answer.

> **Never asked of you:** a check, a measure, a log to read, the result of a CI run. An agent runs them and reports.
> A move is only for what a person must do.

## Questions outside the wish

- **Notifications.** In the window, a question asked in an active wish also shows as a system notification: a click
  shows the wish, and a button answers it, one per option letter, or **Yes**. None shows for the wish the window shows
  while it is in front.
- **The global shortcut**, set in Connections & preferences, brings Djinn forward from anywhere on the wish with the
  newest open question, else on the flying carpet: Ctrl+Alt+Space by default, Ctrl+Cmd+J on macOS. Empty turns it off.

## Common mistakes

- **Reading the options before the recommendation.** The recommendation is the Djinn's answer with its reasons; most
  of the time, rubbing the lamp is the whole of it.
- **Answering without a note when you have a condition.** A word with your choice ("only for invoices over 10 pages")
  shapes the tasks the answer leads to.
- **Treating a grey card as urgent.** A blocking question holds a worker; one that can wait can wait for tomorrow. The
  bar shows you only what holds a wish up.
- **Putting off a blocking question.** Later moves it down the list; its worker still waits.
- **Guessing rather than asking for light.** Enlighten me costs an investigator's time and a little of your agent's
  budget, not yours, and the question comes back better.

## Read next

- [Decision](#decision): what an answered question becomes.
- [Enlighten me](#enlighten): how an investigation runs.
- [The Djinn](#the-djinn): who asks, and how it turns your answers into work.

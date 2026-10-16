# Question and move

A question is how the Djinn, or a worker, asks you for what only you can give: a decision, an answer, or a move. The
Djinn asks rather than guesses, and it talks to you only this way: a report, a doubt, a "done as you asked" all come as
a question in the wish they concern.

Each question comes with the options, the one the Djinn recommends and why, and what is at stake. Once you answer it,
it is a [decision](#decision): kept with who took it, and the tasks it led to.

## Reading a question

A question is a card, coded Q01, Q02 and on in its wish. From top to bottom:

1. **The question**, in a sentence or two.
2. **The recommendation**: the option the Djinn recommends, and why. Read it first; it is often enough.
3. **The options**, A to D, the recommended one marked **Recommended**. A question may have none: then it asks for a
   yes, or for your answer in words. An option that launches a task once picked says so on one grey line under it:
   **Launches:** the task's title, and the azima it belongs to. An option without that line leaves the tasks to the
   Djinn.
4. **What is at stake**: the facts, what each option changes and costs, and, for a move, the exact commands to run.
   **Read more** unfolds it when it is long.

When the question names a report of the wish, the card shows **Report:** with its title, which opens it in place.

## How much it holds up

A question's level says how much it holds up, and every list of what waits for you follows that order:

| Level | What it means |
| --- | --- |
| Blocking | A task waits for the answer: a worker is held until you decide. The card is red. |
| Your move | Something only you can do, needed before a step the Djinn names. |
| To decide | Needed before something, in the Djinn's own words: "before the merge". The card is orange. |
| Can wait | Useful, not urgent. The card is grey. |

**What waits for you**, the bar at the top of each wish and of the flying carpet, lists them across the wish, or across
every active wish, the most blocking first; a click takes you to the card. When the Djinn's agent shows a choice in its
terminal, that choice comes before everything: it holds up the Djinn, so every wish.

## Answering

Under the options, one field takes your note, with three gestures:

- **Rub the lamp** answers with the option selected and your note. The recommended option is selected from the start,
  so one click takes the Djinn's advice; pick another option and the lamp sends that one. With no option recommended,
  pick one first. A question with no options takes your words as the answer.
- **Enlighten me** asks for more before you decide. See below.
- **Withdraw question** closes a question that no longer matters, without an answer, with a note on why. It shows as
  **Withdrawn**, and is no decision.

Your answer goes to the Djinn, which turns it into tasks itself, placing them in the plan. On an option that says
**Launches:**, an answer without a note launches that task at once, when it waits for no other task; with a note, or
a task to wait for, the Djinn reads your note and launches it, as planned or edited. The decision then links the task
it launched, on its own line: **Launched as planned**. If the Djinn is not running,
the answer is kept and the card says **Waiting for the Djinn**: it gets it once it starts.

> **Example.** Q07 asks: "The export takes 40 s on large invoices. Render in the background?" The recommendation is A,
> "Yes, with a notice when the PDF is ready". You agree, type "keep it under 2 s for small ones" in the note, and rub
> the lamp. The Djinn spawns the tasks, and the decision log shows Q07 with your note and the tasks it led to.

### Enlighten me

When you cannot decide yet, **Enlighten me** sends a worker to find out more first: your note says what to dig into,
and an empty note digs in general. The question folds to one line, **Being investigated**, with what you asked, out of
the way of the questions that wait for you; a click opens it, and you may still decide meanwhile. Once revised, it opens
again and waits for your answer, its earlier rounds folded below the card (**History**). See [Enlighten me](#enlighten).

### From the keyboard

With the focus on a card, or in its note:

| Keys | What they do |
| --- | --- |
| Arrows, or 1 to 4 | Pick an option (not while typing in the note) |
| Enter in the note | Rub the lamp: answer with the option selected |
| Shift+Enter | A new line in the note |
| Ctrl+Enter (Cmd+Enter on macOS) | Answer, from anywhere on the card |
| Ctrl+Shift+Enter (Cmd+Shift+Enter on macOS) | Enlighten me, from anywhere on the card |

Enter or Space on an option only picks it. The keys stay on the card: they never take a key from the terminal or
another field.

## Moves

A move is a question about something only you can do, through the window or outside Djinn: push a tag, open or merge a
pull request, start a release, check on a Mac or a Windows machine Djinn does not have, grant a right, install and
restart. Its context starts with the exact commands to run, in order, each with what it prints and what to expect; its
line in **What waits for you** carries a button to where you act, a pull request's page for instance.

Some moves are Djinn's own, asked when Djinn itself needs your go. Your yes then acts on its own:

| Move | What your yes does |
| --- | --- |
| Install and restart a build | Installs that build |
| Push a tag | Pushes the tag |
| Open a pull request | Opens it as a draft, following the project's own way of opening one when it has a skill for it |

The other moves are noted: you make them, and the Djinn reads your answer.

> **Never asked of you:** a check, a measure, a log to read, the result of a CI run. An agent runs them and reports.
> A move is only for what a person must do.

## Everything for you is a question

Whatever the Djinn needs from you comes as a question, even one without options when it really does not know what to
think: its analysis is in the card's context. Reports it wants you to see come as a yes or no acknowledgement, often
with "OK, nothing to do" as an option. Two things follow:

- **An answered question is resolved.** The Djinn does not bring it back; it asks a new one only on a real doubt.
- **A question goes as soon as it is moot.** When a task, or Djinn, does what a question asked, the Djinn withdraws it.

## Questions outside the wish

- **Notifications.** In the window, a question asked in an active wish also shows as a system notification: a click
  shows the wish, and a button answers it, one per option. None shows for the wish the window shows while it is in
  front.
- **The global shortcut**, set in Connections & preferences, brings Djinn forward on the wish with the newest open
  question from anywhere: Ctrl+Alt+Space by default, Ctrl+Cmd+J on macOS.
- **Another wish.** A question that belongs to another wish can move there (**Move to another wish**), the tasks
  following its decision along with it if you choose. It takes the next code in its new wish.

## Good to know

- **Read the recommendation first.** It is the Djinn's answer with its reasons; most of the time, rubbing the lamp is
  the whole of it.
- **Use the note.** A word with your choice ("only for invoices over 10 pages") shapes the tasks the answer leads to.
- **Red first.** A blocking question holds a worker; a grey one can wait for tomorrow.
- **Ask for light rather than guess.** Enlighten me costs a worker's time, not yours, and the question comes back
  better.

## Read next

- [Decision](#decision): what an answered question becomes.
- [Enlighten me](#enlighten): how an investigation runs.
- [The Djinn](#the-djinn): who asks, and how it turns your answers into work.

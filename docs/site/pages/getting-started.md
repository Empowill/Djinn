# Getting started

From nothing to your first wish granted: install Djinn, let it find your agents, give it a project, make a wish, and
see how the Djinn and its workers carry it out while you decide. Each step says what you will see in the window.

## 1. Install Djinn

Djinn installs in your own folders, with no administrator rights. The install is one line to paste in a terminal, one
for macOS and Linux, one for Windows: you find both at the top of the README of Djinn's repository, under Install. The
line picks the build for your system, checks it against the release's checksums, and puts Djinn in your own folders.
On Linux without WebKitGTK, it installs a build that opens Djinn in your browser instead of a window of its own.

When it ends, it tells you how to start Djinn: type djinn up in a terminal. The window opens.

> **Closing the window does not stop Djinn.** The Djinn and the workers go on behind it. To quit, choose Quit Djinn in
> the tray menu, or press Ctrl+Q (Cmd+Q on macOS). Starting Djinn again while it runs brings its window to the front.

## 2. Let Djinn find your agents

Djinn runs the agents you already use: Claude Code, Codex and Antigravity. It asks for no key and stores none: each
agent works with the account you signed it in with, and that account pays for it.

**Connections & preferences**, at the bottom of the side panel, shows one line per agent: whether Djinn finds it, its
version, and whether it is signed in. Where one is missing or signed out, a button installs it or signs it in: it runs
the agent's own command in a terminal of the panel, where you answer if it asks, and Djinn checks again once it ends.
The panel opens by itself when the window starts with no agent ready.

One agent is enough to begin.

## 3. Add your first project

A project is a folder Djinn's workers work in: a Git repository, or a plain folder.

1. Click the **+** next to **Projects** in the side panel: **Create a project**.
2. Choose the folder (**Choose a folder…**; in a browser, type its path) and, if you like, a name.
3. Click the project to see what Djinn found: its folder, whether it is a Git repository, its remote, and its skills.

The terminal at the bottom of the window opens in your first project, never in your home folder: an agent started in
your home folder would ask you to trust all of it.

## 4. Meet the Djinn

The app is Djinn; its lead is **the Djinn**. It is one agent session, Claude Code by default, that leads every active
wish: it talks with you, splits each wish into tasks, starts workers on them, asks the questions and keeps the plans
true. It lives in the terminal of the window, in a tab named **The Djinn**.

- **Talk to the Djinn**, at the top of a wish or of the flying carpet, opens it. The first time, it starts the Djinn in
  your first project; afterwards, it resumes the same session, wherever it was.
- The arrow beside it, **Choose the agent**, starts the Djinn on another agent found on this machine.
- **Write to the Djinn**, the pill at the bottom of a wish, sends it a word about that wish without leaving the page
  (Ctrl+I, or Cmd+I on macOS).

At every start of Djinn, the Djinn comes back on its session. When its agent asks for your approval in the terminal (a
command to run, a folder to trust), the bar of what waits for you shows the choice first, with its options as buttons.

> **The Djinn talks to you in the window, not in its terminal.** Everything it has to tell you, a report or a "done as
> you asked" included, comes as a question in the wish it concerns. A line you type in its terminal is answered in the
> window.

## 5. Make your first wish

1. Click **New wish**, at the top of the side panel. The page asks: *What would you like to make happen?*
2. Say it in a sentence or a few: the outcome you want, not the steps to get there.
3. Pick the **projects it works on**, and the **rights** its workers have there (see below).
4. Click **Make the wish**.

The wish opens on its own page, and the Djinn hears of it. If the Djinn does not run, the page says so before you make
the wish, with **Start the Djinn**. If three wishes are active already, the button reads **Make it paused**: the new
wish waits, set aside, until you resume it.

Click under the wish's title to give it the few lines it opens with: what it is for, its scope, where it goes. The
Djinn reads them every time it looks at the wish.

## 6. Watch the Djinn and its workers

The Djinn plans the wish first. It splits it into [azimas](#azima), the parts of the plan, and into tasks, the pieces of
work, each one after the tasks it needs. Then work starts:

- **Each task gets a worker**: an agent of its own, in a worktree and a branch of its own, so that no two workers step
  on each other. Djinn starts a worker when the machine has room for it, the first wish first.
- **The Tasks tab** shows what moves: each task with its status, its agent and model, how long it ran, what it cost,
  and, for a running worker, its live CPU and memory. Click a task to read what its worker did and said, and to see its
  diff.
- **Workers never commit.** When a task ends, Djinn commits its work. In a project whose settings name its checks,
  Djinn merges that work into the wish's branch, runs the checks, and starts a correction worker when they fail. It
  pushes on a cadence, not once per worker.
- **The Djinn hears of each task's end**, reads what was done, and decides what comes next: the next task, a
  correction, or a question for you.

You can step in at any time: **Send** an instruction to a running worker, **Pause the worker**, **Stop the worker**, or
**Write to the Djinn** about the wish.

## 7. Answer the questions, make the moves

When the Djinn needs you, it asks rather than guesses. A question comes as a card, coded Q01, Q02 and on: the Djinn's
recommendation first, then the options, then what is at stake.

- **Rub the lamp** answers with the option selected: the recommended one until you pick another, so one click takes it.
- **Enlighten me** asks for more before you decide: a worker digs into what your note asks, and the question comes back
  revised.
- A **move** is a question about something only you can do: push a tag, open or merge a pull request, check on a Mac.
  It shows under **Your move**, with what to do.

The bar at the top of the wish, and of the flying carpet, lists what waits for you, the most blocking first. A red
question holds up a task; answer those first. [Question and move](#question-and-move) tells the whole of it.

## 8. Grant the wish

When every task is finished and no question is open, Djinn proposes to grant the wish: **My wish is granted**. Only you
grant a wish, and you may grant it earlier. A granted wish leaves the active ones, and keeps everything: its plan, its
decisions, what each task did.

## What matters to get right

- **Write the wish as an outcome.** What should be true once it is granted, and where it stops. The clearer the
  outcome, the fewer questions you will get, and the better the plan.
- **Give it its projects.** A worker works in the wish's projects only. A wish made without a project can be given
  one later.
- **Choose its rights on purpose.** **The project decides** follows the project's own configuration; **Edit the files**
  lets the workers edit; **Edit, in auto mode** lets them run in their agent's auto mode. In a folder outside Git with
  no rule, a worker only reads until you say it may edit.
- **Name your checks.** A project whose settings name its checks has every task's work checked and merged by Djinn on
  its own. Without them, nothing tests a task's work before it lands.
- **Answer the red questions first.** Each one holds up a task. A question that can wait shows grey; it can.
- **Keep three wishes active, no more.** Pause one to make room: its workers stop where they are, and resume later
  where they were.
- **Let the machine decide how many workers run.** The operating load, at the top of the flying carpet and of the
  Hourglass, sets how much of the machine Djinn takes; on Auto it yields the machine to you while you type.

## Read next

- [Wish](#wish): active, paused and granted wishes, their rank, their projects.
- [Azima](#azima): how the Djinn lays out a plan, and how you validate a part of it.
- [Question and move](#question-and-move): every way to answer, and what each kind of question means.

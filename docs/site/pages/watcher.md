# Watcher

A watcher is a task that runs a command instead of an agent: it follows something outside Djinn, a pipeline, a pull
request, a queue, and each time the command has something new to say, it wakes the Djinn. No model reads its output,
it spends no token, and it takes no slot of the machine.

## Why watchers

Much of the work around a wish happens elsewhere: a CI run that takes twenty minutes, a reviewer who comments
tomorrow, a deployment that ends when it ends. An agent waiting for it would spend its time and its tokens on nothing.
A watcher waits for free, and speaks only when there is news:

- **A pull request babysat.** A failed pipeline, a new comment, a conflict: each wakes the Djinn, which follows the
  project's babysit skill to answer it.
- **A wish that follows something.** A wish template, a QA run for instance, starts its watcher with the wish, and the
  Djinn acts on what it reports.
- **Anything a command can follow.** A queue, a log, a status page, as long as the project allows the command.

## How a watcher works

A watcher's prompt is its command line. Djinn runs it in the project's folder, with no worktree and no branch: a
watcher writes nothing. The command runs without a shell: words group with quotes as in a shell, but a pipe, a
redirection or a variable other than Djinn's own needs a script of its own. Each watcher keeps a folder of its own
from one run to the next, where its command may keep its state.

**What it prints is read in paragraphs.** A paragraph ends after a second of silence, or at two hundred lines. Each
new paragraph:

- is kept in the task's events, and its first line shows under the watcher's row;
- wakes the Djinn with one line: which watcher spoke, the first line of what it said, and whether its command is still
  watching or has ended.

A paragraph identical to the one before it is kept but wakes no one, and after a restart of Djinn, a first paragraph
that repeats the last line seen wakes no one either. A command that prints the same status every minute costs the
Djinn nothing until the status changes.

**How a watcher ends:**

| What happens | What Djinn does |
| --- | --- |
| The command prints its done line (a babysit's or a template's) | The watcher ends, done |
| The command exits normally | The watcher ends, done; with restart on, the command starts again instead |
| The command fails | It starts again, waiting a second, then twice as long each time, up to five minutes |
| It fails five times in a row | The task fails, with the last error lines, and the Djinn is told |
| You stop it | It ends, stopped |

A run that lasted a minute or more counts as a success: the next failure starts the count again. A watcher is never
ended for being silent, which is its normal state, nor for a budget: it runs as long as its wish needs it.

## What a watcher may run

A watcher's command runs with no one to review it, so Djinn checks it before it starts, and again when it resumes.
Where a project lists the commands its workers may run, a watcher runs a command only when:

- the project lists it among those commands, or the wish allows it;
- a skill of the project declares it, as a babysit or a wish template, and the command is the one Djinn builds from
  that declaration; or
- you allowed it yourself, in the project's panel, under **Watcher commands you allow**: type the command, or its
  start, and **Allow**. **Take back** removes it. What you allow is kept in your own settings, never in the repository,
  and holds for the commands that start with it.

A command the project denies never runs, whatever else allows it. A refused watcher says why, and how to allow its
command.

## Babysitting a pull request

When a task opens a pull or merge request, in a Git project whose skills name a babysit command, Djinn starts a watcher
on it by itself once the task ends done:

1. It finds the request: the one the task opened, else the one open for the task's branch, else the one open for the
   wish's branch.
2. It starts a watcher, titled after the request, part of the task's azima, running the skill's command on it.
3. It tells the Djinn once which task opened what and which watcher babysits it, and which skill to follow each time
   the watcher speaks.

A request already babysat is not watched twice: a second watcher is never started, and one the Djinn started by hand
on the same command is adopted. The watcher ends when its command prints the skill's done line, or when the command
exits. Djinn itself does not decide that a request was merged: the skill's command says so.

A project without a babysit skill gets no watcher. A project may also turn babysitting off in its settings.

## Watchers of wish templates

A skill may declare a wish template: a kind of request that comes back, with its own watcher. When a request matches
it, Djinn proposes the template's wish; once you say yes, the new wish starts the template's watcher at once, titled
after the skill, and the Djinn follows the skill.

When that watcher prints its done line, it ends, and Djinn asks you whether to grant the wish, with **Grant the wish**
or **Keep it open**. It never grants the wish itself: the decision is yours.

## What wakes the Djinn

The Djinn does not poll. It waits for the events of its wishes, and each one wakes it with a line:

| Event | What it says |
| --- | --- |
| Ended, failed | A task's worker ended or failed, and what Djinn does about it |
| Merged | A task's work merged into its integration branch |
| Landed | All of an azima's work is in main, and whether every azima of the wish is: then it proposes to grant it |
| Answer, enlighten | You answered a question, or asked to know more before answering |
| Approved | You approved a decision the Djinn took |
| Move | Something left to the Djinn: work that failed to integrate, push checks that stay red |
| Watch | A watcher spoke, ended, or began babysitting a request |
| Request | A request came in for a wish, or an azima was opened to plan |
| Restart | Djinn started again, with the workers it resumes |
| Replied, out | An ifrit's turn ended, or it went out |

Events that come close together, an integration pass merging several tasks for instance, reach the Djinn as one
digest. An event the Djinn has not taken in, because it crashed or its session was lost, comes back: none is lost.
When the Djinn is not waiting for events, Djinn types one short line in its terminal saying how many wait.

Your own words wake it too: **Write to the Djinn**, at the bottom of a wish, types them in its terminal as if you had.
A [block](#block) written for the wish wakes no one.

## Watchers in the window

A watcher shows in the **Tasks** tab as a task, with the status **Watching** while it runs, no model, and the first
line of its latest paragraph under its row. Its events hold every paragraph. It has no box to send it a message: a
watcher reads none.

- **Pause the worker** holds it, **Resume the worker** lets it go on.
- **Stop the worker** ends it.
- In **What runs**, each watcher of a wish has its line below the workers: what it said last and since when, and the
  request it watches, its number a link that opens it in your browser.
- An azima whose only live work is a watcher, the babysit of its pull request for instance, is **Watching**: not
  done, and what comes after it still waits.

## Watchers from the Djinn

Ask the Djinn for a watcher in words: "Watch the nightly pipeline and tell me when it fails", "Babysit W5's merge
request". It spawns a task whose prompt is the command, with restart on when the command should run again after each
normal exit, and with babysit on to take the command from the project's babysit skill. A babysit spawned after a task
watches the request that task opened; if none is open, the watcher fails, saying so.

A watcher cannot be continued: it has no session, and its agent cannot change. To watch something else,
the Djinn spawns a new one.

## Good to know

- **One status line per change.** A command that prints a short line when something changes, and nothing otherwise,
  makes the best watcher: each paragraph is news.
- **Print the done line.** A watcher with nothing to end it runs as long as its wish; a babysit or a template ends on
  its done line.
- **A refused command is not a bug.** Allow it in the project's panel, or list it in the project, and the watcher
  starts.
- **A watcher's done line grants nothing.** The wish waits for your yes.

## Read next

- [Pull and merge requests](#pull-requests): what a babysit follows.
- [Skills and templates](#skills-and-templates): where babysits and templates are declared.
- [The Djinn](#the-djinn): who wakes, and what it does then.

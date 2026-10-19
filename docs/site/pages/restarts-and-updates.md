# Restarts and updates

Djinn can stop at any time: you quit it, your machine restarts, an update replaces it, it crashes. Nothing is lost.
Stopping Djinn interrupts the workers; the next start resumes each one by itself, in the same task, worktree and
session, and the Djinn comes back on its own session. An update never restarts Djinn without your click.

## Why it matters

A wish lives for days, its workers for hours. A Djinn that lost its work at every restart would ask you to choose
between an update and your wishes, or to babysit each worker back to life. Djinn keeps everything in its data folder
and in the projects' worktrees instead, and takes each worker back where it was.

## What survives a restart

| What | After a restart |
| --- | --- |
| Wishes, plans, questions, decisions, blocks, tilasms | Kept whole: they are in Djinn's data folder. |
| A running worker | Resumed by itself, in its task, worktree and session, told that Djinn restarted and that its worktree is as it left it. |
| A worker that had not started yet | Still planned: it starts at its turn. |
| A watcher | Resumed, counted apart. |
| A paused wish's workers | Still paused: they resume once the wish is active again. |
| A worker you paused | Resumed at the next start. |
| A worker you stopped, or stopped for its budget | Stays stopped. |
| The Djinn | Back in its terminal at every start, on its session. |
| A lit [ifrit](#ifrit) | Out: a message lights it again. |
| Investigations you asked for | Picked up again. |

The workers resume in the order of the wishes' ranks, the first wish first, never more at once than the machine has
room for, and before any new task. A worker that never reported a session starts again from its first prompt.

### What is cut

A restart cuts what was running at that moment, and Djinn takes each piece back:

- **A worker's command in progress** stops with the worker; the worker resumes on its session and runs it again if
  it needs to.
- **A check cut midway** records nothing, so it runs again. A check that already passed on the same work is not run
  again.
- **A merge in progress** goes back to waiting, and Djinn integrates the work again.
- **A push in progress** is cut and starts again; one waiting for your go still waits for it.
- **Changes to a repository** get a few seconds to finish before Djinn stops, so that none is left half written.
- **The Djinn's turn** is cut; it resumes on its session and finds the restart in its events.

After a restart, the Djinn is told in one line what happened: how many workers were interrupted, which ones Djinn
resumed and which ones are yours to decide, the watchers resumed, what the stop cut.

### What does not resume

A task is not resumed when it was imported from another machine, when its wish is granted, when its worktree is gone,
or when it was already continued in a fork. It stays **Interrupted**, among the finished parts of its
[azima](#azima), never in what waits for you; **Mark done** closes it. A task whose worktree is gone tells the Djinn,
which can start a new task from its context.

### Nothing left behind

Killing Djinn leaves nothing running behind it: its workers, watchers and checks, and the commands its workers run,
die with it. Djinn records each process it starts while it runs; should one survive all the same, the next start stops
it before it resumes any task, and the Djinn hears how many were stopped. Djinn never touches a process it did not
start.

## The Djinn and the terminals

The Djinn comes back at every start, in its terminal, on its session, however Djinn stopped. The other terminals
depend on how:

- **After an update**, every terminal comes back, on the same sessions, and the window shows again the wish it showed.
  Those that could not start again are listed in the banner, **These terminals did not start again:**, until you
  dismiss it.
- **After a crash**, the Djinn's terminal comes back.
- **After you quit**, only the Djinn comes back.

The terminals that install or sign in an agent never come back.

Djinn's log, djinn.log in the logs folder of its data folder, names what stopped it, on its line **shutdown begins**:
Ctrl+Q (Cmd+Q on macOS) in the window, Quit Djinn in the tray, Ctrl+C or a kill in the terminal that runs Djinn
(SIGINT, SIGTERM), an install or a restart from the window, or a restart from the command line.

## Limits and wakes

A few things stop a worker without a restart, and Djinn handles them the same way:

- **Your agent's usage limit.** The worker shows **Waiting for the limit**, with when the limit resets, and resumes a
  minute after, told that the limit has reset. A limit that names a model holds only the tasks of that agent on that
  model; one that names none holds only the task itself (djinn task start passes it). When the reset time is unknown,
  Djinn tries again after 15 minutes, then waits longer each time, up to 2 hours. Sign the agent in with another
  account meanwhile and Djinn sees it within a minute: the tasks the limit stopped resume at once. Either way, the
  Djinn hears once which tasks resume.
- **A silent worker.** The Lamplighter, Djinn's watchdog, wakes a worker that printed nothing for 15 minutes with no
  process busy and no gate held or awaited, or whose process is lost: it ends it and continues it on its session. After
  three wakes in a row the task fails, saying so, and the Djinn is told.
- **A model that answers nothing.** A worker whose model call returns nothing in the middle of its turn is cut and
  continued on its session: after 5 minutes on Claude Code; after 8 on Antigravity, which retries such a call by itself
  after 5 and keeps its session that way. Antigravity says nothing while one of its commands runs, in the foreground
  or in the background, until it ends: its silence then is cut after 30 minutes only.
- **A worker silent after its result.** A worker whose turn ended, silent since for ten minutes with its processes
  idle and no gate held or awaited, is stopped: done if it wrote its final note, its work then integrated; otherwise
  continued once, told to finish its task and write its final note, and failed if silent so again. One line to the
  Djinn either way. On 17/10/2026 a correction hung so for eleven minutes after its final note, a wait it had sent to
  the background still alive, and every task of its wish waited for it.
- **A watcher** is never ended for its silence, which is its normal state. A command of it that dies starts again; five
  deaths in a row fail it, and the Djinn hears why.
- **A budget.** A task is stopped, "budget exceeded", past 2 hours of work in total across its runs (automatic resumes
  keep counting), or a worker past 500 model turns in one run. Continuing it gives it a new 2-hour and 500-turn budget.

Only the resumes after a usage limit count against a task, and those after a network error when no outage was
confirmed: after three of them, it fails. A restart, a pause, a lost process or a confirmed network outage costs it
nothing.

## A network outage

When the network goes down, Djinn holds what needs it and takes it all back once the network returns, by itself. On
17/10/2026 an outage turned a wish's push checks red: a correction would have repaired code that was not broken, and
every task of the wish waited.

- **It notices from what fails.** The errors that mean no network: a worker's agent that cannot reach its provider (a
  connection error, a request timed out, a reply cut off mid-stream, the API's 5xx or overloaded), git that could not
  resolve its remote's host, gh or glab that could not connect, a name the machine could not resolve. A connection that
  broke in a browser test (network changed, failed to fetch) counts only beside another sign, at the same time: a busy
  machine says it too.
- **It asks once, then every 30 seconds.** Such an error makes Djinn probe the network: one light request to the
  provider's address. Failed, the outage is confirmed; answered, it was not one, and nothing changes. While all goes
  well, Djinn probes nothing.
- **While it is down,** no worker starts; the running ones are paused once the command they run ends, never failed for
  their silence; pushes, fetches and the questions to the forge wait. A worker the outage cut is not failed: it shows
  **Resuming**, network down since 15:12. The status bar says **network down since 15:12**.
- **When it comes back,** Djinn resumes the work one task per pass, repairs first: the paused workers, then the tasks
  the outage cut, each told that the network came back. Push checks that failed on the network run once again instead
  of starting a correction: a network failure is never a code failure. Red again once the network is up, they hold the
  push as any red check does.

The Djinn hears one line as the network goes down, one as it comes back, and has nothing to do in between.

## A worker's health

On each of its passes, Djinn's scheduler judges every worker from what it already knows of it: its last word, what its
processes use of the processor, the gates it holds, its usage limit and its error. A worker is:

| Health | What it means | What follows |
|---|---|---|
| Well | It spoke within ten minutes, or a command of it works the processor, or it holds a gate | Nothing |
| Stalled | It said nothing for ten minutes and its processes stay idle, under 5% of a core | A mark beside it; the Djinn hears it |
| Waiting for its limit | Its agent's usage limit stopped it | It resumes at the reset |
| Limit lifted | The reset passed, or its agent's account changed | Djinn resumes it by itself; the Djinn hears it |
| Can be relaunched | It failed for a reason a new turn may get past: its agent or its stream broke, a stop of Djinn cut it, a usage limit since lifted | A mark beside it; the Djinn hears it |

A worker whose turn ended may still wait for work it started in the background: busy, it is well; idle for ten
minutes, Djinn stops it (see a worker silent after its result, above), and it is stalled only
after thirty minutes when its turn ended on an error or a usage limit. A task stopped on request, or failed for good (its budget, its permissions, a dependency, an error Djinn does
not know), is never flagged.

A flagged task shows a small circular arrow beside its code, on its card and in the tree, amber when stalled, salmon
when it failed; its tooltip says why and how to relaunch it. The Djinn hears it once, in one line with the command
that relaunches it, until its health changes again. Continuing the task relaunches it: a stalled worker is ended and
continued on its own session, as the Lamplighter wakes one.

The thresholds come from nine days of this machine's workers: of 330 817 pauses between two words of a worker, 64 passed
ten minutes; every one whose processes worked 5% of a core or more was a command at work, and the idle ones were an
outage of the agent's service, workers the Lamplighter woke, and workers waiting for their background work after their
turn. The ten minutes after a result come from the runs of one wish of three days, 315 results: the waits after a
result that went on were busy (tests at about two cores, up to 23 minutes), or waited for a usage limit, or, once, idle
for nine and a half minutes before going on; the worker that hung stayed idle for eleven.

## Updates

When a newer Djinn is ready, a banner shows on its own row at the very top of the window:

- **A release to download**: **A new version of Djinn is ready**, with **Update** and, when it has some, **Release
  notes**.
- **A newer Djinn already installed** where Djinn runs from: a line naming its version, **Djinn … is ready**, with
  **Restart**.
- **A build of your checkout**, for those who build Djinn themselves: **New Djinn ready:** with its commit, its
  branch and its wish, what it adds and lacks, **Details**, and **Install and restart**.

Nothing downloads nor restarts without your click. The click stops Djinn as a quit does, workers interrupted and
nothing lost, and starts the newer one, which takes everything back as above. **Dismiss**, the ×, hides an item until
another version comes.

When an update fails, the banner says why, with **Try again**.

### For those who build Djinn

A Djinn built from a checkout of Djinn watches the releases too. With the checkout on its main branch, holding no work
the release lacks, a newer release installs by itself, and the banner then offers **Restart**; a project setting turns
this off. On a branch with work of its own, a release is never installed over it: Djinn merges main into the wishes'
branches instead.

A build Djinn proposes may also come as a question, **Install and restart**: your yes installs it, and the banner then
offers to restart on it. See [Question and move](#question-and-move).

## Common mistakes

- **Killing Djinn to fix a freeze.** Nothing is lost either way, but what Djinn was waiting on is lost with it: have
  Djinn write where each of its parts waits first, a dump its debug command makes, so that the freeze can be fixed.
- **Expecting an update to start by itself.** It never does: the banner waits for your click, so a restart never cuts
  your work by surprise.
- **Restarting to hurry a worker.** A restart interrupts every worker, each one's command in progress included; a
  worker that seems stuck is the Lamplighter's to wake, or the Djinn's to relaunch once its card shows it stalled.
- **Looking for an interrupted task in what waits for you.** A task that could not resume is history: it is among the
  finished parts of its azima.

## Read next

- [Machine and load](#machine-and-load): how many workers resume at once.
- [The Djinn](#the-djinn): how the Djinn comes back.
- [Task and worker](#task-and-worker): a worker's states.

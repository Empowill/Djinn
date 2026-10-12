# Djinn user guide

How to get going with Djinn, then use its flight plan day to day. Djinn is one command, `djinn`: a window, a local
server, and a command line your agents use to drive a wish. You say what you wish; a lead (Claude Code, Codex or
Antigravity) splits it into tasks; workers do them; you answer the questions and grant the wish.

> **Alpha.** Djinn is under development: see the warning at the top of the [README](../README.md).

<picture>
  <source media="(prefers-color-scheme: light)" srcset="screenshots/readme/questions-light.png">
  <img alt="A wish in Djinn: what waits for you, and a question with the lead's recommendation" src="screenshots/readme/questions-dark.png">
</picture>

## Install

The one-line install is in the [README](../README.md#install); it works once the first release is out. Until then,
build Djinn from this repository: [`CONTRIBUTING.md`](../CONTRIBUTING.md#getting-set-up).

Workers and leads run the agents you already have, as you installed and signed in to them:

- Codex: `npm install -g @openai/codex`
- Claude Code: `npm install -g @anthropic-ai/claude-code`
- Antigravity, optional: [`providers.md`](providers.md#antigravity).

**Connections & preferences** (bottom of the side panel) shows, one line per agent, whether Djinn finds it, its
version, and whether it is signed in (`claude auth status`, `codex login status`; Antigravity has no such command, so
its sign-in shows as unknown), with a button to install it or sign it in. The button runs the agent's own command in a
terminal of the panel, where you answer if it asks; once it ends, Djinn checks again. The same panel opens from the
arrow beside **Lead** (**Set up the agents**), and by itself when the window opens with no agent ready. Djinn looks
where its workers and leads look: its PATH, your login shell's, and the installers' folders. Djinn asks for no API key,
and stores none: the account each agent signs in with pays for it.

## Start Djinn: `djinn up`

```sh
djinn up              # the window
djinn up --browser    # no window: prints the URL to open in your browser
```

- **One Djinn per data folder.** A second `djinn up` brings the running window to the front.
- **The data folder.** It is `djinn` in your system's configuration folder (`~/.config/djinn` on Linux,
  `~/Library/Application Support/djinn` on macOS, `%AppData%\djinn` on Windows). `DJINN_HOME` sets another one.
- **Closing the window does not stop Djinn.** The lead and the workers go on. To quit: "Quit Djinn" in the tray menu,
  Ctrl+Q (Cmd+Q on macOS), or stop `djinn up`.
- **How many workers at once.** The machine decides: one per 2 cores and per 2 GiB of memory.
  `djinn up --workers 4` (1 to 16) sets it. The status bar shows `Workers 2/4`, and whether the page is `Live`.
- **Worker CPU and memory limits.** On Linux, workers run in systemd user scopes. CPU can be capped with
  `--worker-cpu` (e.g. `150` for 1.5 cores). Memory limits can be set with an expert static ceiling (`--worker-memory`)
  or an adaptive guardrail based on observed provider peaks (`--worker-memory-guard 2.5`): [worker memory](worker-memory.md).
- **Operating load slider.** Controls how much of the machine Djinn uses: five manual notches (**Minimal**,
  **Medium**, **High**, **Max**, **Overclock**), or **Auto** (`light` is accepted as an alias for `medium`).
  The UI displays a dual gauge forecast: CPU (target workers and standard versus low CPU priority) and Memory
  (peak memory Djinn may engage against current usage and total machine RAM). The **Overclock** notch pushes up to
  150 % of standard memory capacity with an amber visual pulse. In Auto mode, Djinn dynamically follows developer
  activity: keyboard and mouse inactivity from the desktop session, combined with the CPU and memory load of programs
  outside Djinn. When you type or move the mouse, or other programs put the machine under heavy load, the notch drops
  directly to Minimal to yield the machine to you; when idle for a few minutes with little load, it slowly climbs back
  towards your chosen ceiling. Where no session idle source is available (SSH, headless, unsupported desktop), it falls
  back to host load alone. In Auto, lowering the notch lowers CPU priority and stops starting new workers, never
  pausing running ones (Decision Q124 (C)). From the command line: `djinn load get`, `djinn load set minimal` (or any notch),
  and `djinn load watch`.
  Each notch engages a worker's memory at a percentile of its readings (a running worker at its own, a new one at the
  median of the latest 10 finished workers' value), plus a margin; builds and tests spike a worker's memory, and the
  gates already run them one at a time. The memory Djinn may commit counts its running workers' own memory once, back
  into what it could use with no worker running. `djinn load get` and `djinn machine show` say it (`worker_engaged_at:
  engaged at p50 (overclock)`):
  - **Minimal**: p95, 512 MiB margin (`light`, an alias for `medium`, takes Medium's).
  - **Medium**: p80, 512 MiB margin.
  - **High**: p70, 384 MiB margin.
  - **Max**: p60, 256 MiB margin.
  - **Overclock**: p50, 256 MiB margin.

### The terminal

The window has a terminal at the bottom. It opens in the folder of your first project (the first of the side panel),
never in your home folder: an agent started there would ask you to trust all of it. With no project yet, the terminal
asks you to create one. `djinn up --terminal-dir <folder>` picks another folder, and
`djinn up --terminal "…" --terminal-dir <folder>` runs that command instead of your shell.

It holds tabs. The window's own terminal is always there; while the lead runs, its tab, **Lead**, is there too. The + of the tab bar, or Ctrl+Shift+T anywhere in the window, opens a new shell
(**Shell 1**, **Shell 2**…); the terminal icon next to a project in the side panel opens one at its root. Each tab you
opened closes with its ×, and comes back after a reload. The terminal folds away; **Restart** starts one again once
its program ended, and on the lead's tab it starts the lead again as **Lead** does. Ctrl+Shift+C and Ctrl+Shift+V copy and paste.

## A project

A project is a folder Djinn's workers work in. Git or not: a folder outside Git works too.

- In the window: **Create a project** (the + next to PROJECTS), then the folder (**Choose a folder…** in the window;
  type the path in the browser) and, optionally, a name.
- From a terminal: `djinn project add <folder>`.

Click a project to see its folder, whether it is a Git repository, its remote and its skills, those summoned from
another project included ("from app"). `djinn skill summon app/babysit-mr --into infra` lets infra's workers use a
skill of app without a copy.

## A wish

**New wish** (top of the side panel) opens **Make a wish**: what you wish, in a sentence, and the projects it works
on. Or from a terminal, or from your lead: `djinn wish make "…"`. Under the wish's title, a click gives it the few
lines it opens with: what it is for, its scope, where it goes (`djinn wish describe <wish-id> --text "…"`). Clicking
the title allows you to rename it (`djinn wish rename <wish-id> --title "…"`). Beside the title, a star button marks
it as the **Principal** wish (**Mark as principal**): its lead coordinates and receives answers from wishes without an active
lead (`djinn wish principal <wish-id> --principal`). When unset, the active wish with the lowest rank is principal.

**Three wishes are active at most.** That guards your attention; the machine decides how many workers run. You may
have as many wishes as you like: with three active already, a new one is made paused (**Make it paused**). The side
panel lists the active wishes by rank, the first has priority: drag one, or Alt+Arrow, to change its rank. The paused
wishes follow, then the granted ones.

At the top of a wish:

- **Pause** sets it aside: its workers stop, each on its own session, and its place frees up.
- **Resume** makes it active again: its workers start again where they were, without counting as a resume. With three
  active already, it takes the last place, and the last active wish is paused. Dragging a paused wish onto an active
  one makes it active at that rank; **Make it the first active wish**, next to it in the side panel, puts it first.
- **Reopen** brings back a granted wish.
- The bin, **Delete this wish**, deletes it for good after you confirm: its tasks and their events, its questions and
  its blocks. Its running workers stop first. A worktree with no work of its own goes;
  one with commits or changes not committed stays, and every branch stays.

From a terminal: `djinn wish pause <wish-id>`, `djinn wish activate <wish-id>`, `djinn wish move <wish-id> --to 1`,
`djinn wish rename <wish-id> --title "…"`, `djinn wish principal <wish-id> --principal`, `djinn wish delete <wish-id>`.

## The lead

Djinn has a single lead for all active wishes: an agent session that talks with you, splits wishes into tasks, asks
the questions and keeps the plans true. It drives Djinn with the `djinn` command.

**Lead**, at the top of a wish, opens the lead in the terminal of the window and focuses on that wish:

- the lead has a session: Djinn resumes it (`claude --resume …` or `codex resume …`) in the folder it was recorded
  in, or shows it if it runs, and types a short line focusing on the wish;
- it has none: a new Claude lead starts in your first project, never in your home folder. With no project at all,
  nothing starts: create one first.

The arrow beside **Lead**, **Choose the agent**, starts a lead of another agent found on this machine: Claude Code,
Codex or Antigravity. An agent that is not installed shows greyed, saying so; the recorded one is checked, and picking
it resumes the lead, focused on this wish. Starting a lead of another provider replaces it.

Every new lead, of any agent, starts by running `djinn brief`: every active wish at once (each wish's title, description,
open questions, what runs and waits, its azimas), then Djinn's rules and the projects' (`AGENTS.md`, `CLAUDE.md`,
`CONTRIBUTING.md`). `djinn wish brief <wish-id>` gives one wish's detail. No model writes it.

The lead talks to you only in the window: everything it has to tell you, a report or "done as you asked" included,
is a question in the wish concerned, often a yes/no acknowledgement with "OK, nothing to do" as an option, and it
withdraws one as soon as a task or Djinn does what it asked. When it does not fully understand an answer, it asks
again before starting any work. It writes nothing for you in its terminal; a line you type there is answered in the
window.

From a terminal, `djinn lead resume` does what **Lead** does, and starts Djinn if it does not run;
`djinn lead resume --provider codex` picks the agent. `djinn lead set <session-id> --directory <folder>`
records a session you started yourself. `djinn lead tell "…"` writes to the running lead as if you typed it in its
terminal, line breaks kept (`--wish-id <wish-id>` says which wish it is about); it waits while you type there or a
choice is on screen. In a wish, the pill **Write to the lead** at the bottom does the same about that wish
(Cmd/Ctrl+I opens it; Enter sends, Shift+Enter breaks the line, Escape folds it and keeps the draft).

When the lead's agent asks for your approval in its terminal (a command to run, a folder to trust), the bar of what
waits for you shows it first, in the flight plan and in every wish, with its options as buttons, and a notification
says so. A button answers in the terminal as its keys would, only while the terminal still shows that very choice: a
different command needs a new click.

When the lead could not start, from **Lead** or as Djinn starts, or its program ended (it failed at once, or you quit
it), a small dot on **Lead** says so, in every wish: its tooltip gives the error or the exit code. The lead's tab keeps its last output, and
reopening it starts nothing. Only **Lead**, or `djinn lead resume`, starts it again; the dot goes once it runs.

Djinn takes the lead back at every start, in the lead's terminal. The first start after an update from a Djinn that had
one lead per wish keeps the lead of the first active wish as the one lead.

## The flight plan

**Flight plan**, at the top of the side panel, shows every active wish together:

- a bar at the top while something waits for you, the most blocking first; a click takes you there;
- the [inbox](#the-inbox), when a source found something;
- each wish at a glance: its questions, what runs, or `READY`;
- what waits for you, in every wish: open questions, workers that wait, wishes Djinn proposes to grant, and the
  **Proofs you can give** (see [azimas](#azimas));
- the questions you asked the lead to investigate.

Each line names its wish; an answer, a stop or a grant goes back to it. Like a wish, the flight plan has a **Tasks**
tab and a **Decisions** tab, with the tasks and decisions of every active wish. An empty section is hidden.

## A wish's view

The **Wish** tab: what waits for you (**Your move**), the questions being investigated, the journal (**Show the
commands**), and the **Rights** of its workers in each project. Beside it, the **Tasks**, **Review**, **Decisions**
and **Tilasms** tabs, and last, discreet, **For agents**.

**Rights** says what the wish's workers may do in a project, for every task to come: **The project decides** (its
own configuration), **Edit the files**, or **Edit, in auto mode**. A worker in a folder outside Git with no rule
reads until you say it may edit: it shows as **Waits for your answer**.

### The Tasks tab

What moves or waits for someone first (running, failed, then waiting and paused, the planned ones last), then the
finished work part of no azima, folded under **Finished**, then the azimas with the work part of them, the done ones
folded, with what they spent. Finished work part of an azima shows under it.

Each task is displayed as a two-line row:
- The first line displays the status badge first (**Running**, **Waits for your answer**, **Planned**, **Done**,
  **Failed**, etc.), its code `W1`, `W2`…, and its title, with the full prompt on hover.
- The second line gives its project, agent provider and model, duration (or waiting time: for a slot, dependency,
  or gate), its model turns against its seal's limit (`37/500`, its run's; it turns orange past 80%, and its
  hover gives the total over its runs), token counts and cost estimate, and for a running worker its live CPU and
  memory usage (not measured on Windows yet).

Clicking a task opens its card body: when it started and ended, the files it writes, its budget, its folded prompt,
diffs, its worker's last word, where its work stands, its events, and folded terminal logs with **See logs** (or
**Hide logs**).

- **Send** an instruction to a running worker, in the box under its events. "Received" shows once the worker said
  something after it.
- **Pause the worker** holds a running worker where it is and frees its slot; **Resume the worker** lets it go on.
  None on Windows yet.
- **Stop the worker** ends it.
- **Mark done** closes a task no worker runs (planned, cut short, failed, stopped, imported) once its work is done,
  with why, in a few words. On a task **Done with problems**, it closes it as seen.

A task whose worker ended done while its permissions refused some of its calls, with nobody to approve them, is
**Done with problems**, amber, not a clean Done: it may not have done its job. It stays with what moves or waits, and
waits for you in the attention bar, until it runs again or is marked done.
- A task that comes from a decision links to it in the Decisions tab.

The status is one colour, one icon and one word: Running, Waits for your answer, Planned, Done, Done with problems, Failed,
Interrupted, Stopped, Paused, Waiting for the limit, Resuming, Resumed as another task, Watching.

### Usage and cost estimates

Every task, wish, and the flight plan show token breakdowns (input, output, cache read, thinking) and an estimated cost computed from an embedded price table (`internal/usage/prices.yaml`), marked "estimated, prices of <table date>".

- **Estimated vs. reported**: Claude reports monetary cost directly in its stream, which Djinn preserves and shows labelled "reported" alongside the estimate. For providers without stream costs (Codex, Antigravity), Djinn converts tokens to cost using the price table.
- **Comparable across providers**: A switch in usage views toggles between currency ($) and tokens, so comparisons across models and providers remain consistent without flattening when a model has no price.
- **Missing prices**: Models without public verified pricing in the table show tokens and "no price in the table". Wish and provider totals note how many tasks have no price.
- **Local overrides**: The embedded table loader leaves a clear seam for local overrides from `~/.config/djinn/prices.yaml` (or `$DJINN_HOME/prices.yaml`) without requiring configuration UI today.

`djinn task pause <task-id>` holds a worker where it is and frees its slot; `djinn task resume <task-id>` lets it go
on (not on Windows yet). A worker that holds or waits for a gate is not paused.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="screenshots/readme/tasks-light.png">
  <img alt="A wish: its tasks and what they cost" src="screenshots/readme/tasks-dark.png">
</picture>

_The two screenshots show a demonstration wish, with fictional data._

### Azimas

The lead's plan is a graph of azimas, coded `T01`, `T02`…: parts of the plan that no worker runs, which work is part
of. The Tasks tab displays the azimas in a single column from top to bottom, each row taking the full width to show
its code, goal, state, progress (parts done, running, and waiting), time, and dependencies, with the work part of it
folded underneath. The tasks inside an azima may flow in columns on a wide screen, but the azimas themselves stay in
a single list. The top of the wish counts the azimas done before the tasks.

The order of azimas is stable between renders and between days, so an azima stays at the same place: first the azimas
that move (a worker running), then those waiting for the developer (awaiting proof / to validate), then the ready ones
not started, then blocked ones, then drafts, and last the done ones folded at the end. Within each group, azimas follow
the plan's code order, never an order by time or by counts that change every minute.

An azima is **Draft**, **Open**, **In progress**, **To validate** or **Done**. A draft azima holds tasks planned for later:
**Open this azima** (`djinn task open <azima>`) turns it open, ready for parts to be spawned, and notifies the wish's lead.
**Move** (`djinn task move <azima> --wish <wish>`) moves an azima (a draft, or an azima no worker runs with parts that have
not started) to another wish.

Each wish has a **Push strategy**: **Per wish** (one integration branch per wish and project) or **Per azima** (one
branch per azima, named `djinn/<code>-<slug of its title>`: `djinn/T07-the-orchestrator`). In the wish header, the push
strategy can be switched, or set from a terminal with `djinn wish push-strategy <wish-id> --strategy wish|azima`. For an
azima with its own branch, status badges show **PR proposed**, **PR open** or **PR merged**, with buttons to open or
merge the PR. An individual **Push** button (`djinn project push <project> --wish <wish>`) pushes the branch on demand
when you choose.

**Where work lands.** Per wish, a wish's work lands in its integration branch, in each project the branch your checkout
was on when the wish was made: the chip beside the push strategy names it (with the project, when the wish has several),
and a click on it changes it to another local branch or to a new one made from it (`djinn wish set-integration <wish>
--branch feat/x`, `--create-branch` for a new one), refused while a merge into it runs. Per azima, each azima's work
lands in main through its own branch and pull request, which its card shows once opened, and work of no azima in the wish's
integration branch; `djinn wish brief` and `djinn brief` say it in a line for each wish.

An azima's branch works like a wish's integration branch. Djinn cuts it from main when the azima is made (an azima made
before its wish was switched gets its branch when its first part starts), and records it on the azima: its card, opened,
shows the branch, the commit of main it was cut from, and that it lands in main. The azima's work merges into it, and Djinn keeps it up with
main by the same rules as a wish's branch ([keeping up with main](team-settings.md#keeping-up-with-main)). It lands in
main: its pull request, opened when you say so after Djinn proposes it, targets main and follows each push of the
branch; Djinn never re-targets it.

Under the per-azima strategy no azima waits for another: each branch holds main and its own work, nothing else. Djinn
refuses an azima after another, directly or through their work (`djinn task spawn --after`, `djinn task depend`,
`djinn task update`, `djinn task group`, an `after:` line of a plan file at `djinn plan sync`), and refuses switching a
wish to per azima while its azimas depend on each other: the window says which in a line, and the switch stays on per
wish. Tasks inside one azima wait for each other as usual. A branch made before, stacked on the branch of the azima it
depended on, keeps working as it is: Djinn never cuts it again nor rewrites it.

Once every task part of an azima is finished, it is **To validate**: nothing is left for Djinn. Its card says what
validating it takes, box by box of its plan file (a person, a machine, a release or a real model gives each proof), and
its **Validate** button marks it done. An azima whose plan file says done stays **In progress** while work part of it
still runs or waits. The flight plan lists the proofs a person can give among the **Proofs you can give**.

### The Review tab

The wish's merged work, a line per task, the merges Djinn started under the task they serve; the tab counts what is
left to review. A click on a line opens its diff, with the comments on its lines; its box marks it reviewed, kept with
who checked it and when, and the lead reads it in its brief and with `djinn mark list`. What is reviewed folds at the
end. **See the diff** stays on each task's card in the Tasks tab.

### The Decisions tab

Every decision, the latest first, read only: an answered question, or a block of kind `decision`. Each row says who
took it (you, the lead, or a worker) and the tasks it led to.

### The Tilasms tab

A tilasm is the material that explains a wish: a folder with an `index.html` (a page, a diagram, a demonstration),
coded `L01`, `L02`…, versioned, and kept in Djinn's data folder, never in a project. The lead makes them with
`djinn tilasm put`; you may drop a folder or a `.zip` on the tab.

**Open it here** shows one in a frame of its own: its scripts run, the network and Djinn stay out of its reach. The
tab searches the tilasms' titles and text, lists each one's **Versions** (**Restore this version** makes an older one
the latest again), and **Export as a .zip** writes one to your Downloads folder. Each tilasm says what it explains:
the azimas and tasks it cites. Its agents draw its diagrams by hand, in SVG, and keep in its folder all it needs, its
fonts and scripts: it reaches no network.

A tilasm's link, `djinn://tilasm/<id>`, opens Djinn on it from a browser, a chat, a terminal or a Markdown file, and
starts Djinn if it does not run; so does `djinn://wish/<id>` for a wish. Inside the window, such a link in a block, a
question or the brief opens in place.

## Questions

The lead asks you through Djinn, never by guessing. A question card, coded `Q01`, `Q02`…, gives the lead's
recommendation first, then the options A to D, then what is at stake. Its colour says how much it holds up: red
when a task waits for the answer, orange under the words the lead gave ("before the merge"), grey when it **Can wait**.
The cards, the bar of what waits for you and the brief list them in that order.

When work leaves a move only the developer can make (push a tag, open or merge a PR, start a release, check on a Mac or
Windows, install when needed), Djinn or the agent asks it as a question with the exact command.

- **Rub the lamp** answers with the option selected and your note: the recommended one is selected first, so one
  click takes it; pick another and the lamp sends that one. With no option recommended, pick one first. The lead
  turns your answer into tasks itself, in one pass, holding the plan's graph. A question without options takes a written answer, sent the same way.
- **Enlighten me**, on its left, starts an investigator worker to dig first, in one click (`enlighten_workers: false`
  lets the lead investigate itself): the note you typed says what to dig into (empty, it digs in general). The question
  folds to one line, **Being investigated** with what you asked, out of the way of the questions that wait for you; a
  click opens it, and you may still decide now. Revised, it opens again and waits for your answer. Its rounds fold
  below the card.
- **Keyboard shortcuts.** Focus on the card, or in its note field, drives it from the keys:
  - Enter rubs the lamp: answers with the option selected, everywhere on the card, the note field included. An
    input method composing does not answer, nor does Enter answer before an option is selected.
  - Shift+Enter makes a new line in the note field.
  - Ctrl+Enter (Cmd+Enter on macOS) also answers, never a new line.
  - Ctrl+Shift+Enter (Cmd+Shift+Enter on macOS) triggers Enlighten me from anywhere on the card, the note included,
    sending what the note asks.
  - Arrows (Up/Down/Left/Right) or 1–4 pick an option when focus is on the card and not in the note.
  The shortcuts stay on the card: they never intercept keys from the terminal or other inputs.

Your answer goes to the lead and the workers. An answered question is a decision. From a terminal,
`djinn question answer <question> <choice>` answers a question; Djinn marks the lead terminal with `$DJINN_LEAD`,
so an answer typed by the lead is attributed to it without echoing back to its own terminal, whereas an answer
typed in a plain shell is attributed to the developer and reaches the lead. When a question is answered in
any wish, Djinn queues the answer for the one lead, which waits on `djinn lead watch`: nothing is typed in the lead's
terminal while its watch runs, so nothing lands in your prompt box; without a watch, Djinn types one short line,
"Djinn: N events waiting, run djinn lead watch". If no lead runs, Djinn marks the question as waiting for a lead until
the lead acknowledges it, and the brief lists it under **Open answers without a lead.**

An open question can be moved to another wish (`djinn question move <question> --wish <wish>`), with `--follow` to
bring tasks following this decision along with it. A question no longer relevant can be withdrawn without an answer
(`djinn question withdraw <question> --note "…"`), marked **Withdrawn**.

**From anywhere.** In the window, a question asked in an active wish also shows as a system notification: a click
shows the wish, a button answers it (one per option, or **Yes**). The browser shows none; on macOS, notifications need
the app bundle. The **Global shortcut**, in Connections & preferences, brings Djinn forward on the wish with the
newest question (Ctrl+Alt+Space by default, Ctrl+Cmd+J on macOS; empty turns it off).

**A request that belongs elsewhere.** When something unrelated comes up, the lead hands it to Djinn, which asks you
"Where does this request go?": file it in an existing wish, keep it where it is, or open a new one: the window opens it,
and the one lead is told of it. Djinn ranks the wishes by their titles, projects and latest blocks, without a model.

## Everything for you is a question

Whatever the lead needs from you comes as a question, even without options when it really does not know what to
think: its analysis is in the card's context. A question you answered is resolved: the lead does not bring it back,
and asks a new one only on a real doubt.

On an open question, **Mark read** tells the lead you have seen it, without a word; the lead reads it in its brief and
with `djinn mark list`.

### The For agents tab

What the lead and the workers write down for one another is a block: a hand-off, a reference, a report. Blocks are
not for you: they wait in the last tab of the wish, **For agents**, with their count, each folded under its title. A
click opens one: Markdown, shown as written, where a `mermaid` code block is drawn as a diagram, which **Expand**
enlarges. A block of kind `decision` shows in the **Decisions** tab instead, the log blocks in the journal.

## Grant a wish

When every task is finished, none done with problems, and no question is open, Djinn proposes to grant the wish:
**My wish is granted**. Only
you grant a wish; you may grant it earlier. From a terminal: `djinn wish grant <wish-id>`.

## Share a wish

**Share**, at the top of the wish, exports it to a `.djinn` file in Downloads: its plan, questions, decisions, blocks,
tilasms and journal. Never code (it travels through Git), never a secret, never a local path.

**Import a wish** (side panel), or `djinn wish import <file>`, opens it on another machine. Each project is found by
its remote, then by its name; one not found waits for `djinn project add <folder>`. Check its projects before you
resume it.

The wish's page, one HTML file standing alone that runs no script, comes from a terminal: `djinn wish render
<wish-id>` writes it once, `djinn wish sync <wish-id>` keeps it up to date while `djinn up` runs (delete the file to
stop).

## Watchers and wish templates

A *watcher* is a task that runs a command, with no agent and no model: a pipeline, a merge request, a queue. Each
new paragraph it prints wakes the lead. Its card shows the first line of its last paragraph; **Pause the worker**
holds it, **Resume the worker** lets it go on. The project must allow the command.

A *wish template* turns a request that comes back (babysit a pull request, a QA run) into a wish that already knows
how to work: a skill declares it in its `SKILL.md`. When a routed request matches it, the new wish starts its watcher
and its lead follows the skill. When the watcher prints its "done" line, Djinn asks whether to grant the wish; it never
grants one itself. How to write one: [wish templates](wish-templates.md).

## Babysitting pull requests

When a task opens a pull or merge request (`gh pr create`, `glab mr create`), Djinn watches it at once, in the same
wish: once the task ends, a watcher task starts on it, part of the task's azima, and the lead hears `W5 opened MR
!123: W7 babysits it`. Each change the watcher prints (a failed pipeline, a comment, a conflict) wakes the lead, which
follows the project's babysit skill; the watcher ends when the request is merged. A request already babysat is not
watched twice. The project names its watcher in one of its skills (`metadata.djinn.babysit`); without one, nothing
starts. `babysit: false` in the project's settings turns it off. The lead can start one by hand: `djinn task spawn
<wish> --title "Babysit W5's MR" --babysit --after W5`. How to declare it: [babysitting what a task
opens](wish-templates.md#babysitting-what-a-task-opens).

## The inbox

A skill may also declare a *source*: a command that prints what comes from outside, like the merge requests assigned
to you. Each item becomes a card in the **Inbox**, at the top of the flight plan, with where Djinn proposes to send it:
a wish to file it in, or a new one. The side panel counts the new items, and the window shows each one as a system
notification. Nothing is made until you click; **Rub the lamp** takes the recommended route, **Dismiss** sets the item
aside for good. Djinn only reads a source: it never comments, reacts or replies. `djinn inbox list` lists the items.
Details: [the inbox](wish-templates.md#the-inbox-what-comes-from-outside).

## Team settings and integration

A repository may share defaults for its workers in `.agents/settings.txtpb`: which agent runs them, which model, how
much one may spend. Your own file, in Djinn's data folder, wins over the team's; a task's own flags win over both.
`djinn project show <project>` says where each setting comes from. Details: [team settings](team-settings.md).

When a project's settings name its checks, Djinn brings finished work into the wish's branch by itself: as each task
ends, it merges the task's branch in a worktree of its own (made ready by the `setup` command), runs the checks named
for `commit` through their gates, and moves the branch when they pass. Each task says where its work stands: waiting
to be committed, committed, conflict, red, corrected by another task. A conflict in code or a red check start a
correction worker; past two attempts, Djinn asks you.

Each project defines its push cadence: **Standard** (pushes at an azima's end or after an hour and three tasks committed)
or **On demand** (pushes only when you click **Push** or run `djinn project push <project> [--wish <wish>]`). You can
switch cadence in the project view or with `djinn project set-push <project> standard|on_demand`. Before it pushes the branch,
it runs the checks named for `push`: red, the push is held, and Djinn starts a correction worker; if that fails or attempts
are spent, it asks you. `djinn project show` and the project's view in the window list the checks and how each last ran.
When the settings also name an install command, the window proposes the build it pushed, one per integration branch:
on one line, its commit, the branch and wish it comes from, and against the running Djinn the tasks it adds, or "older
than the running Djinn on 3 commits" when the running Djinn holds commits it lacks; **Details** unfolds the tasks merged
since, each with its title and folded summary, and the commits' titles, folded. The build of the branch the running
Djinn was built from comes first; the other branches' builds are folded under one line that names them, each with its
own **Install and restart** and ×. Nothing installs before your click; × hides that build until a newer one comes. With `install_questions: when_needed`,
Djinn raises no routine install question: the banner and post-push install are enough, and it asks only when necessary
(e.g. a build carries a fix for a task you are waiting on, a change that takes effect after restart and matters now, or
the running build is broken), saying why in the question; with `install_questions: never` (set in Djinn itself), no
install questions are asked. Details: [integration](team-settings.md#integration).

Djinn also keeps the wish's branch up with the project's main branch: it fetches main at most hourly, and once main
holds a release the branch lacks, it merges main in the same way, tested, never rebasing; a conflict starts a
correction worker. The wish's head shows the last merge, "origin/main merged into feat/x: 4 commits, v0.2.0". The
settings `main_branch`, `merge_main` (at each release, at each commit, or never) and `merge_main_minutes` change it.
Details: [keeping up with main](team-settings.md#keeping-up-with-main).

## Gates

Workers share one machine. Heavy commands (tests, builds, code generation) run through a gate of the running Djinn:

```sh
djinn gate run test -- go tool task test
```

A gate has one holder at a time, and is granted only while the machine is not under pressure, the first wish by rank
first; a command measured before waits until the machine has the memory it peaked at. The status bar shows how many
gates are held. `djinn command list` shows what each command cost (CPU time, peak memory, duration).

## When Djinn stops and starts again

Nothing is lost. Stopping Djinn interrupts the workers; the next `djinn up` resumes each one by itself, in the same
task, worktree and session, in the order of the wishes' ranks and within the machine's slots. It does not resume a
task imported from another machine, a task of a granted wish, or a task whose worktree is gone; after three resumes, a
task fails. Such a task is history: it shows among the finished parts of its azima, never in what waits for you.
`djinn task continue <task-id> --prompt "…"` gives it a new turn of its own session, or **Mark done** closes it.

A worker stopped by its provider's usage limit shows "Waiting for the limit", then resumes once the limit resets.

The Lamplighter, Djinn's watchdog, wakes before it fails. A worker that prints nothing for 15 minutes, with no
process busy and no gate held or awaited, a worker whose stream goes idle, or whose process is lost, is ended and
continued on its session; only after three wakes in a row does its task fail, saying so, its lead told once. A
watcher (`--provider watch`) is supervised apart, for as long as its wish needs it: never ended for its silence,
which is its normal state, nor for a budget; a command of it that dies starts again in the same task, and only five
deaths in a row fail it, its lead told why. A budget is no wake: a task is stopped, "budget exceeded", past 2 hours
of work in total across its runs, or a worker past 500 model turns in one run, and stays stopped; `djinn task
continue` gives a stopped worker 500 turns again, not more time. These budgets are part of the task's
[seal](providers.md#a-tasks-seal). A Claude worker's count grows as it works; Antigravity
reports its own at the end of each answer, Codex none.

At every start, the lead of the first active wish comes back on its session, in its folder. The lead terminals come
back after an update or a crash too, on the same sessions; not after you quit. When a newer Djinn is installed, a
banner offers **Update**, with its **Release notes** when it has some: nothing restarts without that click. A Djinn
built from a checkout is named as a build is (its commit, branch and wish, what it adds and lacks); × hides it until
another version is installed. After the
restart, it lists the terminals that did not start again.

A Djinn you built from your checkout of Djinn (`go tool task install`) watches the releases too. With your checkout
on main, a newer release that holds your build installs by itself (the project setting `install_releases`, on by
default), and the banner then offers to restart on it; on a branch with work of its own, a release is never installed
over it: Djinn merges main into the wishes' branches instead, and proposes their build once the release is in
([releases of Djinn](team-settings.md#releases-of-djinn-for-those-who-build-it)).

`djinn backup` copies the data folder, even while Djinn runs: [backups](backup.md).

## More

- **Documentation**, in Connections & preferences: **Open** displays the documentation window over the interface,
  synchronized with the window's theme (also served at `/docs/` while `djinn up` runs).
- [Agent protocol](agent-protocol.md) · [Providers](providers.md) · [Adaptable wishes](adaptable-wishes.md) · [Worker memory](worker-memory.md)
- The language follows the system (English or French); change it, and the theme, in **Connections & preferences**.
- To build Djinn, or work on it: [`CONTRIBUTING.md`](../CONTRIBUTING.md).

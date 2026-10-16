# Project and settings

A project is a folder Djinn's workers work in: an application's repository, a service, a set of documents. It may be a
Git repository or not. A [wish](#wish) works on one project or several, and every task of the wish runs in one of
them.

A project brings two things with it. Its **settings** say how its workers run and what Djinn does with their finished
work: which agent and which model, how much a worker may spend, which checks run before work is committed or pushed.
Its **permissions** say what a worker may do there: edit the files, run which commands.

## Why projects

Djinn works on your code where it lives, and every repository has its own habits: its tests, its lint, its main
branch, its way of opening a pull request. A project is where those habits are written down once, so that every worker
of every wish follows them without being told, and so that a team shares them through the repository itself.

## Adding a project

In the side panel, the **+** next to Projects, **Create a project**, opens **Add a project**:

1. **Folder**: the project's folder. **Choose a folder…** picks it in Djinn's window; in a browser, type its full path.
2. **Name**, optional: the folder's name when left empty.
3. **Add the project**. The window says **Project app added.**, and the project shows in the side panel.

Two projects never share a name, nor a folder, case ignored. The folder must exist on this machine.

Djinn's command line adds a project too: see the [Command line](#commands).

> **Example.** You add the folder of your web application as "app". The side panel lists it; a click on it shows its
> folder, Git repository, its remote on GitHub, its two checks (lint before a commit, tests before a push) and its
> skills. You make the wish "Let customers export their invoices as PDF" on app.

### In Git or not

A project in a Git repository, or in a folder inside one, gives each worker a **worktree and a branch of its own**,
under Djinn's data folder, started from the tip of the wish's [integration branch](#integration-branch). Workers never
touch your checkout, and two workers never step on each other. Djinn writes nothing into your folder but Git's own
record of the worktrees.

A folder outside Git works too: its workers edit the folder itself, keeping apart by what each one writes. No branch,
no merge, no push: what a worker writes is there when it ends.

### A project with no folder here

A wish imported from another machine, or Djinn's data restored from a backup, may name a project whose folder this
machine does not have. The side panel marks it **No folder on this machine**, and the window says the project is not on
this machine. Adding its folder, as above, attaches it to that project instead of making a new one: Djinn matches it by
its remote, then by its name.

## The project's view

A click on a project in the side panel opens its view:

| Part | What it shows |
| --- | --- |
| Problems | In red at the top: a settings file Djinn cannot read, and what it ignored. |
| Folder | Its path, and whether it is a Git repository. |
| Remote | Where Djinn pushes, when the repository has one. |
| Push cadence | **Standard** or **On demand**, and each branch Djinn pushes that differs from its remote, with a button to push it. See [Push](#push). |
| Checks | What Djinn runs on finished work, and how each last ran: passed on which commit, when, in how long, or why it failed. |
| Watcher commands you allow | The commands a [watcher](#watcher) may run here for you, with **Allow** and **Take back**. |
| Skills | The project's skills, and those it uses from another project, marked **from** that project. |

The side panel also shows, under each project, the branches waiting to be pushed, and a button that opens a terminal
at the project's root.

Nothing removes or renames a project once added.

## Settings: the team's file and yours

A project's settings come from two files, setting by setting:

- **The repository's file**, in its agents folder (the settings file next to its permissions file), committed with the
  code: the team's defaults, the same for everyone who works on it.
- **Your own file**, in Djinn's data folder, one per project, never committed: what you want differently on this
  machine.

For each setting, the first that sets it wins:

1. **The task's own choice**, for the agent, the model and the budget only: when the Djinn spawns a task with a given
   model, that model runs it.
2. **Your own file.**
3. **The repository's file.**
4. **Djinn's default.**

Two rules make it predictable:

- **A file that names the agent names its model too**: its own model, or that agent's default. Your file choosing Codex
  never runs Codex with the team's Claude model.
- **A file that names checks names them all.** Your checks replace the team's list; they are not added to it.

Djinn reads both files each time a task is spawned, from the project's folder as your checkout has it, not from any
worker's worktree. A task keeps what it got: changing a file changes the tasks to come.

### What the settings say

| Setting | What it decides | When unset |
| --- | --- | --- |
| Agent and model | Which agent runs the workers (Claude Code, Codex, Antigravity), and its model. | Claude Code, its default model |
| Budget | The most one worker may spend, when its agent can enforce it. | No limit |
| Branch names | How each worker's branch is named, from the task's code, its title and a unique part. | Code, title, unique part |
| Checks | What Djinn runs before it commits a task's work, before it pushes, or both. See [Gates](#gates). | None: see below |
| Setup | What makes a fresh worktree ready, such as installing dependencies. | None |
| Generated files | Files code generation makes, and the command that makes them: a conflict only there is settled by running it again. | None |
| Targeted tests | How a worker runs only the tests covering what it changed. | The worker finds them |
| Correction attempts | How many correction workers Djinn starts on failed work before it asks you. See [Merges and corrections](#merges-and-corrections). | Two |
| Main branch | The branch others merge into, and how often and when Djinn merges it into a wish's branch. See [Integration branch](#integration-branch). | The remote's default, else main, else master; at each release; looked at hourly |
| Push cadence | Standard or on demand. See [Push](#push). | Standard |
| Push strategy | One branch per wish, or per azima. See [Push strategy](#push-strategy). | Per wish |
| Babysitting | Whether a pull request a task opens is watched by itself. See [Pull and merge requests](#pull-requests). | On, when a skill declares how |
| Install | What installs the build Djinn pushed; the window then proposes it. | Nothing proposed |
| Investigators | Whether **Enlighten me** sends a worker, and which agent, model and budget it gets. See [Enlighten me](#enlighten). | On; Sonnet under Claude Code; two dollars each |

Two settings are read from **your file only**, never the repository's: the commands you allow a watcher, and those you
allow an ifrit's serve. A repository that sets them is warned, and ignored: it lists its commands in its permissions.

### Checks make Djinn integrate

The checks are what turn on Djinn's handling of finished work. With at least one check, Djinn merges each task's work
into the wish's branch, checked, and pushes it once its push checks pass. With none, the project's view says **No
check**: Djinn merges finished work only into a wish's own branch, untested, never into the main branch. See
[Merges and corrections](#merges-and-corrections).

### Changing a setting

- **From the window**: the push cadence switch, and the watcher commands you allow, in the project's view. Both write
  your own file.
- **For the team**: edit the repository's file and commit it, like any change of the project, or ask the Djinn for a
  task that does.
- **For yourself**: your own file, or Djinn's command line, whose project details list every setting with where it
  comes from: the repository, your file, or the default. The window does not say where a setting comes from.

### A file Djinn cannot read

A file that does not parse, a value out of range, two checks of the same name: Djinn still starts, and the project's
view shows the problem in red, with the file and the line. New tasks in the project wait, saying **settings
unreadable** and why, and start once the file is fixed; Djinn holds the merges there meanwhile. A team's budget never
silently vanishes.

A setting this Djinn does not know, written by a newer one, is only a warning: Djinn ignores that line.

### No secret, ever

The repository's file is shared and versioned: it holds no key. Djinn refuses a file, its comments included, where a
setting's name speaks of a key, a token, a secret or a password, where a word starts like a well-known key, or where a
long run of letters and digits looks like one. The problem gives the line, never the value. A worker's keys stay where
its agent keeps them.

## Permissions

The repository's permissions file, in the same agents folder, says what its workers may do: whether they may edit,
which commands they may run (each a command or its first words), which they never may, whether they may use the
network, and whether they run in auto mode.

What a worker may do is decided in this order, the first that applies:

1. **A task with no project** only reads.
2. **The wish's rights** in the project, **Edit the files** or **Edit, in auto mode**, decide whether it edits. They
   never add a command, nor the network: the permissions file still says which, and its refused commands stay refused.
3. **The permissions file**, when the project has one.
4. **The agent's own configuration**, in a Git repository or a folder that has one.
5. Otherwise, the worker **only reads**, and Djinn asks you: *May the worker of W4 change the files of app?* **Yes**
   starts it again, allowed to edit and to run no command; **No** keeps it reading. The question also comes as a
   notification, **Permission asked**, which you may answer from the notification itself.

The wish's **Rights**, on its Wish tab, say for each project what its workers may do, on top of the project's own
configuration; **The project decides** gives no right of its own. See [Rights](#rights).

### What no worker does

- **Push.** In a Git project, every push a worker tries goes nowhere: Djinn alone pushes, after the checks. See
  [Push](#push).
- **Commit into the wish's branch.** A worker commits on its own branch; Djinn brings its work into the wish's branch.
  See [Merges and corrections](#merges-and-corrections).

A command a worker's permissions refuse is not put to you: Djinn notes it, and tells the Djinn at the worker's end how
many calls were refused. The Djinn then decides whether the wish needs a broader right, and asks you if so.

## Good to know

- **Commit the team's file.** The repository's settings and permissions are read from your checkout as it is: a change
  not committed applies on your machine only, and a teammate's Djinn never sees it.
- **Name the checks.** A project with none has its work merged untested into a wish's own branch only, and never pushed
  checked: name at least the fast one, before a commit, and the slow one, before a push.
- **Your file replaces, it does not add.** Checks you name in your file replace the team's whole list.
- **A task keeps its settings.** A change to a file applies to the tasks spawned after it, not to those already made.
- **No secret in the files.** Djinn refuses the file outright, even a comment.

## Read next

- [Rights](#rights): what a wish's workers may do in a project.
- [Integration branch](#integration-branch): where a wish's work lands.
- [Gates](#gates): how the checks run without overloading the machine.

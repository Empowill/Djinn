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
> configuration: its folder, its remote on GitHub, its reference branch main, its two checks (lint before a commit,
> tests before a push), each from the team's file, and its skills with their role. You make the wish "Let customers
> export their invoices as PDF" on app.

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

## The project's configuration

A click on a project in the side panel, on its name or on its gear, opens its configuration, as Djinn reads it on this
machine. Under its name, its folder, its remote and whether it is a Git repository; then one box per part, what waits
for you first:

| Part | What it shows |
| --- | --- |
| To fix | In red, first, only when something waits for you: what Djinn finds that you must settle, each in one sentence, the error as Djinn read it, and what settles it. A source to decide on has its two choices, **Plug in** and **Say no**, each with what it changes; a summoned skill no longer found has **Unsummon**; a file to correct names the file; a command that does it is there to copy. See below. |
| Reference branch | The branch Djinn reads the project's configuration on, keeps the wishes up to date with, that azimas are cut from and that pull requests aim at: its name, the ref Djinn merges it from and the commit it points at, and where its name comes from (Djinn's default, the team's file or yours). Under it, **Where workers start**, for each push strategy, the project's own marked: per wish, from the wish's integration branch, the reference branch for a new wish; per azima, an azima's work from its own branch, cut from the remote's reference branch, and work of no azima from the wish's integration branch. Never from your copy. When your copy has another branch checked out, a line says so. **Change** lists the remote's branches as your copy last fetched them; **Choose** writes the one you pick in your own file, never in the repository, and when the choice is yours the list offers to take it back. See below. |
| Worth knowing | The information Djinn finds: nothing waits for you. |
| Checks | The setup and the checks Djinn runs on finished work, where each comes from, and how each last ran: passed on which commit, when, in how long, or why it failed. |
| Inbox sources | The sources its skills declare on this machine: what each reads, how its last run went, and **Plug in** or **Unplug**, with what that does. See [Inbox](#inbox). |
| Skills | The project's skills, the folder each lives in, and those it uses from another project, marked **from** that project. Each says its role for Djinn: the wish template it offers, the inbox source it declares, the babysit of the project's pull requests; or know-how for the workers alone. A skill Djinn cannot read, or cannot use in part, sends you to To fix. |
| What changed on the reference branch | The journal of changes: each commit of the reference branch that changed the configuration, when, which and who, and what it changed in sentences. One that waits for your look has the flame and **Seen**; the others say they were applied. When Djinn could not fetch the branch, it says why, as git said it. See below. |
| Push cadence | **Standard** or **On demand**, where it comes from, and each branch Djinn pushes that differs from its remote, with a button to push it. See [Push](#push). |
| Settings | Every other setting, its value and where it comes from: those a file sets first, those at Djinn's default folded under them. Hovering where a value comes from shows the file. |
| Watcher commands you allow | The commands a [watcher](#watcher) may run here for you, with **Allow** and **Take back**. |

No button of the configuration is in the accent colour: none is a recommendation.

The side panel also shows, under each project, the branches waiting to be pushed, a button that opens a terminal at the
project's root, and its gear.

Nothing removes or renames a project once added.

## What Djinn finds in a project

Djinn reads a project's configuration each time it shows the project, and says what it finds in a list: things to fix,
which wait for you, then information. Each one says what it is in one sentence, and the one action that settles it: a
file to correct, a source to decide on, a skill to unsummon, a reference branch to name. The projects list carries the
number of things to fix: while there is one, the project's gear in the side panel turns red with a small flame, its
tooltip counts them, and the [inbox](#inbox) gives the project one line that opens its configuration. Information
leaves the gear grey: you read it on opening the configuration.

- **Things to fix**: a settings file or the permissions Djinn cannot read; no reference branch found, or the one the
  settings name missing from the repository; the setup that failed its last run; a skill whose header Djinn cannot
  read, or whose wish template, inbox source or babysit it cannot use; a summoned skill no longer found; an inbox
  source this machine has not decided on, plugged in or said no to; a change on the reference branch that waits for
  your look.
- **Information**: a field a newer Djinn knows, ignored; your copy on another branch than the reference branch, which
  does not change the configuration Djinn reads, nor where its workers start; a source plugged in that no skill declares any more; a line of a skill's
  header that breaks its format, which Claude Code reads anyway and Djinn reads around.

Unplugging a source, even one never plugged in, says no to it: its project no longer asks. Plugging it in forgets the
no. The command line's project details list the same notices.

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
into the wish's branch, checked, and pushes it once its push checks pass. With none, the project's configuration says
**No check**: Djinn merges finished work only into a wish's own branch, untested, never into the main branch. See
[Merges and corrections](#merges-and-corrections).

### Changing a setting

- **From the window**: the push cadence switch, the reference branch (**Change**, then **Choose**), and the watcher
  commands you allow, in the project's configuration. Each writes your own file.
- **For the team**: edit the repository's file and commit it, like any change of the project, or ask the Djinn for a
  task that does.
- **For yourself**: your own file, or Djinn's command line, whose project details list every setting with where it
  comes from: the repository, your file, or the default. The window does not say where a setting comes from.

### The reference branch

The reference branch is the project's main: Djinn keeps the wishes up to date with it, cuts azimas from it, and pull
requests aim at it. By default it is the remote's default branch, else main, else master. To follow another one, dev
say, choose it under the reference branch of the project's configuration, or with the project command set-branch of
Djinn's command line: it goes to your own file, so nothing is committed behind the team, and it wins over the team's.
Djinn refuses a branch the repository does not hold, on its remote when there is one: fetch it first. Taking your
choice back gives the team's file its say again, else Djinn's default. A team that follows dev names it in the
repository's file, for everyone.

Djinn reads the project's configuration on the reference branch, as your copy last fetched it: the team's settings, the
permissions and the skills. A branch checked out in your folder, or a change not pushed yet, does not change what Djinn
runs the project with; a fix counts once pushed and fetched, and a message about a file Djinn cannot read names the
branch it read it on. The commands still run in the project's folder. A project outside Git, without a remote, or whose
reference branch is not fetched yet, is read in its folder.

### The watch on the reference branch

Every five minutes, and after each of its pushes, Djinn fetches the reference branch of each project, that branch alone,
and compares the digest of its agents folder and its Claude skills folder with the one of its last look: nothing is read
again while it has not moved. When it has, each commit that changed them becomes an entry of the project's journal of
changes, told in sentences: a new check before a push, a setting changed, two more commands the workers may run, a new
skill with a wish template. Past twenty commits in one look, the older ones are told together; a change of the
reference branch itself is one entry, the whole difference. The first look tells the last five as history.

A change is applied as soon as Djinn fetches it. The gear lights only when a decision comes back to you: the
permissions give the workers more (a command allowed or no longer denied, editing, the network, the agent's own auto
mode), a skill's wish template or babysit is new or changed, or a source you plugged in on this machine runs another
command. Mark it **Seen**, in the journal or under To fix, or acknowledge it from the command line: the gear goes out.
A new source asks through its own line, until you plug it in or say no. A new check, a skill's new instructions or a
setting changed only show in the journal.

### A file Djinn cannot read

A file that does not parse, a value out of range, two checks of the same name: Djinn still starts, and the project's
view shows it in red, a thing to fix with the file and the line. New tasks in the project wait, saying **settings
unreadable** and why, and start once the file is fixed; Djinn holds the merges there meanwhile. A team's budget never
silently vanishes.

A setting this Djinn does not know, written by a newer one, is only an information: Djinn ignores that line.

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

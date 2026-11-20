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
> settings: its folder, its remote on GitHub, its reference branch main, its two checks (lint before a commit, tests
> before a push) from djinn.yaml under Gates and checks, and its skills with their role. You make the wish "Let customers
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

A click on a project in the side panel, on its name or on its gear, opens its settings: a page of the window, like the
Summoning Circle, as Djinn reads them on this machine. Under its name, its folder, its remote and whether it is a Git
repository; the reference branch and the commit Djinn read; and where the values come from, the repository's
djinn.yaml and your own file (each file's path on hover). **To fix** stays above the tabs, first, only when something
waits for you or Djinn warns: in red, what Djinn finds that you must settle; after it, quieter, what Djinn ignores in a
file and goes on without. Each in one sentence, the error as Djinn read it, and what settles it: a source to decide on
has **Plug in** and **Say no**, a summoned skill no longer found **Unsummon**, a file to correct names the file. See
below.

Each setting shows in a table with three values: the **repository**'s, as djinn.yaml sets it; the **local** one, in
your own file on this machine; and the one **retained**, with where it comes from. A side that does not set it says
**absent**; a repository's value your file overrides is struck.

| Tab | What it shows |
| --- | --- |
| General | Worth knowing, the information Djinn finds. The **reference branch**: its name, the ref and commit Djinn reads, where its name comes from; **Change** lists the remote's branches and **Choose** writes the one you pick in your own file, never in the repository. The **branches**: the reference branch and the names of the workers' branches. The **life of a worktree**: **Prepare**, required, the setup that makes a fresh worktree ready, with its last run; **Install after a push**, optional, on or off: on, once Djinn pushed the integration branch the window proposes to install it; off without an install command. Writing an empty install in your own file turns it off on this machine alone. |
| Worker defaults | What the project gives a worker when neither its task nor the [Summoning Circle](#the-window) names anything, with a link to the Circle, which comes first: agent, model, budget per task, correction attempts, Enlighten me; the question workers' agent, model and budget; the **ceilings of the containers** its workers start, set in djinn.yaml only. |
| Gates and checks | The **checks** from djinn.yaml, when each runs and how each last ran; the targeted test command; the **named gates** djinn.yaml declares, each with its commands alone and beside, read-only, and how a command is matched. A gate can be turned off on this machine, and back on, in your own file; it is added only in the repository. See [Gates](#gates). |
| Integration and push | The push cadence, **Standard** or **On demand**, and each branch Djinn pushes that differs from its remote (see [Push](#push)); the branch of new wishes, per wish or per azima, a default each wish changes in its own settings, with where workers start: never from your copy; main brought in and how often; the generated files and the command that makes them again; the babysit of pull requests, which a skill of the project does, not Djinn itself; when to ask to install, and whether releases and builds install by themselves. |
| Workers' brief | What each worker of the project gets at its launch. First the **instructions by kind of task**, the general text, then work, investigation, mockup and ifrit, each with its value in the Summoning Circle, in djinn.yaml and in your own file, the one retained and where it comes from; a lower layer a higher one overrides is struck, and a kind that takes the general text says so. **Set mine** or **Edit mine** writes your text for a kind in your own file, **Remove** takes it back: see Workers' instructions, below. Then what Djinn adds, as it stands, to the first prompt of each worker that edits the project, drawn from its settings: the checks it runs and when, the targeted tests, the setup of a fresh worktree. |
| Skills | The project's skills, the folder each lives in, and those it uses from another project, marked **from** that project, each with its role for Djinn: a wish template, an inbox source, the babysit, or know-how for the workers alone. Under them, the **inbox sources** they declare on this machine, with **Plug in** or **Unplug**. See [Inbox](#inbox). |
| Permissions | The repository's permissions file as read on the reference branch: editing, what is not listed, the network, the commands allowed and denied. **Where it applies**: Djinn translates it for the agent of each worker of the project, at launch. Claude gets an allow or deny rule per command, and refuses the rest in listed mode; Codex has each command's approval accepted by Djinn only when listed, in listed mode, its own reviewer deciding in auto mode; Antigravity gets a grant per command, every command in auto mode. A watcher or an ifrit's serve that Djinn runs itself runs a listed command, one a skill declares, or one you add; a denied command never runs. It does not apply to a question worker, a task outside any project, nor a project without the file. Then the **additions on this machine**: the commands you allow a watcher and an ifrit's serve, with **Allow** and **Take back**, in your own file. See [Rights](#rights). |
| Changes on main | The journal of changes: each commit of the reference branch that changed the configuration, when, which and who, and what it changed in sentences. One that waits for your look has the flame and **Seen**, and the tab counts them; the others say they were applied. When Djinn could not fetch the branch, it says why, as git said it. See below. |

No button of the configuration is in the accent colour: none is a recommendation.

The side panel also shows, under each project, the branches waiting to be pushed, a button that opens a terminal at the
project's root, and its gear.

Nothing removes or renames a project once added.

## What Djinn finds in a project

Djinn reads a project's configuration each time it shows the project, and says what it finds in a list: things to fix,
which wait for you, then warnings, then information. Each one says what it is in one sentence, and the one action that settles it: a
file to correct, a source to decide on, a skill to unsummon, a reference branch to name. The projects list carries the
number of things to fix: while there is one, the project's gear in the side panel turns red with a small flame, its
tooltip counts them, and the [inbox](#inbox) gives the project one line that opens its configuration. Information
leaves the gear grey: you read it on opening the configuration. So does a warning, shown under To fix after the
things to fix, with an amber sign and in quieter text: Djinn ignores something in a file and goes on without it. The
heading of To fix counts the things to fix alone, as the gear and the inbox do; with only warnings, it carries no
count.

- **Things to fix**: a settings file, the permissions or the djinn.yaml Djinn cannot read; no reference branch found, or the one the
  settings name missing from the repository; the setup that failed its last run; a skill whose header Djinn cannot
  read, or whose wish template, inbox source or babysit it cannot use; a summoned skill no longer found; an inbox
  source this machine has not decided on, plugged in or said no to; a change on the reference branch that waits for
  your look; a folder where Claude Code cannot keep the sessions of the project's workers, its name too long for its
  file system (see [restarts and updates](#restarts-and-updates)).
- **Warnings**: a field of djinn.yaml Djinn does not know, ignored; a second djinn.yaml, not read.
- **Information**: a field a newer Djinn knows, ignored; your copy on another branch than the reference branch, which
  does not change the configuration Djinn reads, nor where its workers start; a source plugged in that no skill declares any more; a line of a skill's
  header that breaks its format, which Claude Code reads anyway and Djinn reads around.

Unplugging a source, even one never plugged in, says no to it: its project no longer asks. Plugging it in forgets the
no. The command line's project details list the same notices.

## Settings: the team's file and yours

A project's settings come from two files, setting by setting:

- **The repository's file**, its djinn.yaml in its agents folder, under settings, committed with the code: the team's
  defaults, the same for everyone who works on it. Djinn no longer reads the settings.txtpb it once kept there: one
  left behind is a warning under To fix, until its settings move under settings in djinn.yaml and it is deleted.
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

Your own file stands in for a repository whose reference branch has no djinn.yaml yet, or an older one: Djinn reads the
team's file there alone, never on the branch you checked out or installed from. Any setting written in your file
applies on this machine, setting by setting, whatever that branch holds: the install command and when to ask to
install, say, so that the window offers again to install the builds Djinn pushes.

Djinn reads both files each time a task is spawned, from the project's folder as your checkout has it, not from any
worker's worktree. A task keeps what it got: changing a file changes the tasks to come.

### What the settings say

| Setting | What it decides | When unset |
| --- | --- | --- |
| Agent and model | Which agent runs the workers (Claude Code, Codex, Antigravity), and its model; the corrections and reviews Djinn starts by itself too, whatever agent the repaired task ran on. | Claude Code, its default model |
| Budget | The most one worker may spend, when its agent can enforce it. | No limit |
| Branch names | How each worker's branch is named, from the task's code, its title and a unique part. | Code, title, unique part |
| Checks | What Djinn runs before it merges a task's work, before it pushes, or both. See [Gates](#gates). | None: see below |
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
| Workers' instructions | What each kind of worker is told at its launch, besides Djinn's own words: see below. | The Summoning Circle's, else nothing |

Two settings are read from **your file only**, never the repository's: the commands you allow a watcher, and those you
allow an ifrit's serve. A repository that sets them is warned, and ignored: it lists its commands in its permissions.

### Workers' instructions

What the project's workers are told, besides what Djinn says itself, set by **kind of task**: a general text, and one
for each kind that wants its own, work, investigation, mockup and ifrit. Each worker reads its kind's text right after
its prompt, at its launch and when it starts again. A correction, review or question worker gets the general text; a
watcher, which runs a command, gets none. Nothing is set by agent: each agent has its own instructions outside Djinn.

The texts come in three layers, kind by kind, as the other settings do, plus one below them:

1. **Your own file**, on this machine, wins over the two others.
2. **The repository's file**, under instructions in the settings of its djinn.yaml, for the whole team.
3. **The Summoning Circle**, in its tab Workers' instructions: this machine's default for every project.

A kind that no layer sets takes the general text retained; with no general text either, the workers get nothing more.
A text written empty counts as set: it tells its kind nothing, over the layers below. Each text holds 2,000 characters
at most. The project's Workers' brief tab shows the three layers of each kind and edits yours; the Circle edits its own;
the repository's changes by a commit. On the command line, the instructions method of the project command shows a
project's and sets yours, and that of the lead command shows and sets the Circle's.

### Checks make Djinn integrate

The checks are what turn on Djinn's handling of finished work. With at least one check, Djinn merges each task's work
into the wish's branch, checked, and pushes it once its push checks pass. With none, the project's configuration says
**No check**: Djinn merges finished work only into a wish's own branch, untested, never into the main branch. See
[Merges and corrections](#merges-and-corrections).

### Changing a setting

- **From the window**: the push cadence switch, the reference branch (**Change**, then **Choose**), the bulk model
  switcher for open tasks, and the watcher commands you allow, in the project's configuration. Each writes your own file.
  The **Summoning Circle** (below the Grimoire in the side panel) sets the model of each kind of worker across all
  projects, stored in your own settings, and previews or switches all open tasks in one gesture; its second tab sets
  the workers' instructions of this machine. The project's Workers' brief tab writes your own instructions by kind.
- **For the team**: edit the repository's file and commit it, like any change of the project, or ask the Djinn for a
  task that does.
- **For yourself**: your own file, the window, or Djinn's command line, whose project details and project
  configuration list every setting with where it comes from: the repository, your file, or the default.

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

To try a change of the configuration before it merges into a protected branch (a gate, a check, a container ceiling,
a skill), name the branch that holds it as your configuration branch, with the project command set-branch and its
config flag: it goes to your own file only, never the repository's. Djinn then reads the djinn.yaml and the skills on
that branch, as your copy last fetched it; the watch fetches it at each of its looks, so a push there counts at the
next one. The permissions stay read on the reference branch, so that no agent's
commit widens its own rights; pull requests, the merges of main and the worktrees keep the reference branch too. The
project's configuration in the window, and its details on the command line, say "configuration read from" that
branch, "not" the reference branch, with the command that takes it back. Once the branch merged and was deleted from
the remote, the watch's next look drops it from your copy, and Djinn reads the reference branch again by itself, and the notice says so until you take your choice back.

### The watch on the reference branch

Every fifteen minutes, Djinn fetches the reference branch of each project, with its tags, and after each of its pushes
the reference branch of the project it pushed to; then it compares the digest of its agents folder, its Claude skills folder and its djinn.yaml with the one of its last look:
nothing is read again while it has not moved. When it has, each commit that changed them becomes an entry of the
project's journal of changes, told in sentences: a new check before a push, a setting changed, two more commands the
workers may run, a new skill with a wish template. Past twenty commits in one look, the older ones are told together; a
change of the reference branch itself is one entry, the whole difference. The first look tells the last five as history.

A change is applied as soon as Djinn fetches it. The gear lights only when a decision comes back to you: the
permissions give the workers more (a command allowed or no longer denied, editing, the network, the agent's own auto
mode), a skill's wish template or babysit is new or changed, or a source you plugged in on this machine runs another
command. Mark it **Seen**, in the journal or under To fix, or acknowledge it from the command line: the gear goes out.
A new source asks through its own line, until you plug it in or say no. A new check, a skill's new instructions or a
setting changed only show in the journal.

### How often Djinn fetches

Djinn fetches only what its work needs, and shares each fetch: a branch the watch, the merge of main or a worker's start
fetched lately is not fetched again by another, and two of them asking at once share one fetch. Main comes with its tags
in one fetch, and the branches of a wish's azimas that are due come together, one fetch for the lot; when that fetch
fails, a branch the remote no longer holds say, each is fetched alone, as before.

The pace follows your presence, never the hour of the day. Once you have touched nothing in Djinn for fifteen minutes
(a key, a click or a scroll in the window, a command you type) and no worker runs, Djinn is idle: the watch looks
every hour, and each wish keeps up with main every three hours at most. As soon as you come back, the window brought to
the front or your first touch, Djinn looks at the configuration and at main at once, at most once every five minutes.

A worker never starts from a stale branch, whatever the pace: before its worktree is made, Djinn fetches the branch it
starts from unless it was fetched within the last minute, and workers that start together share that fetch. Merging
work and pushing are not slowed either: they fetch what they need when they need it.

### A file Djinn cannot read

A file that does not parse, a value out of range, two checks of the same name: Djinn still starts, and the project's
view shows it in red, a thing to fix with the file and the line. New tasks in the project wait, saying **settings
unreadable** and why, and start once the file is fixed; Djinn holds the merges there meanwhile. A team's budget never
silently vanishes.

A setting this Djinn does not know, written by a newer one, is ignored, and the rest of the file applies: in your own
file it is only an information; in the repository's djinn.yaml, a warning that names it under settings.

In djinn.yaml, a setting is written as YAML, each under settings: a value of a list may stand alone, generated: gen/**
say, and a choice by its last word, in any case, when: commit, install_questions: never or push_strategy: azima. Djinn's
own repository names there its setup, npm ci, what go tool task gen makes and how, its two checks, the lint before a
commit and the tests before a push, how to install a build, and never to ask a question to install one.

### No secret, ever

The repository's file is shared and versioned: it holds no key. Djinn refuses a file, its comments included, where a
setting's name speaks of a key, a token, a secret or a password, where a word starts like a well-known key, or where a
long run of letters and digits looks like one. The problem gives the line, never the value. A worker's keys stay where
its agent keeps them.

## Djinn's own file

A repository tells Djinn about itself in a YAML file of its own, djinn.yaml, at its root or in its agents folder.
Djinn reads it on the reference branch, as it reads the permissions: a worker never changes what bounds it. When both
are there, the one at the root is read, and a warning under To fix says the other is not. A field Djinn does not know
is ignored, and the rest of the file applies: a warning under To fix names the file and the field, more quietly than a
thing to fix, and leaves the gear grey. A file that does not parse, or a value of the wrong type or out of range, is a
thing to fix: none of the file applies, the project's tasks do not spawn, and To fix names the file and the error.

The file says the **version** of its schema, first: version 1, which a file without it is. Each Djinn knows versions up
to its own, and reads an older one lifted to its own first. A file of a higher version, written for a newer Djinn, is
refused rather than read wrong, as a version that is not a whole number from 1 is: To fix names the file, the version
it read and the highest it knows, none of the file applies, and the project declares no gate until Djinn is updated or
the file fixed.

It holds the **team's settings**, under settings (above), the project's **gates** (Gates), and the **ceilings of the
containers** the workers start with Docker: the memory of each container, swap
included, in MiB (containers, then memory_mib), and its processes (containers, then pids). They stop a runaway, a fork
bomb or a leak, never a container's ordinary work: a project whose containers need more gives them more here. Without
the file, each container gets 4 GiB, or a quarter of the machine's memory when that is less, and 1024 processes.
Djinn's Docker, first on each worker's path, brings a higher limit down to these ceilings, refuses an update that
would lift them, and names the file where the project raises them.

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

- **Commit the team's file.** The repository's djinn.yaml and permissions are read on the reference branch: a change
  not pushed applies nowhere, and a teammate's Djinn sees it once fetched.
- **Name the checks.** A project with none has its work merged untested into a wish's own branch only, and never pushed
  checked: name at least the fast one, before a commit, and the slow one, before a push.
- **Your file replaces, it does not add.** Checks you name in your file replace the team's whole list.
- **A task keeps its settings.** A change to a file applies to the tasks spawned after it, not to those already made.
- **No secret in the files.** Djinn refuses the file outright, even a comment.

## Read next

- [Rights](#rights): what a wish's workers may do in a project.
- [Integration branch](#integration-branch): where a wish's work lands.
- [Gates](#gates): how the checks run without overloading the machine.

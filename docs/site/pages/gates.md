# Gates

A gate is a named turn on the machine for a command: the tests, a build, code generation, an end-to-end run. Workers,
Djinn's merges and pushes, the Djinn and your own terminal all run their commands through the gate of the command's
name. A gate is granted only while the machine can take it. A **heavy** command holds its gate **alone**, one holder at
a time; a **light** one **shares** it with the other light commands of that name.

Besides the gates, every other command a Claude worker runs that neither reads nor edits is **admitted**: it waits
only while the machine cannot take it, then runs beside the others. Djinn measures each one, and a command measured
heavy takes its turn, one at a time.

## Why gates

Workers share one machine. Five workers starting the whole test suite at the same minute would each run five times
slower, run out of memory, or fail on a port another one holds. Gates turn that crowd into a queue:

- **Heavy commands one at a time.** Two heavy test suites never run side by side, whatever their gates; two light lints
  may.
- **Only when the machine can take it.** A gate waits while the machine is under pressure, and until it has the memory
  the command needed last time.
- **The most urgent first.** Djinn's merges and pushes go first, then the first wish's commands, the work of its first
  azima first.
- **Measured.** Djinn records what each command cost, and knows it again next time, run by another task in another
  folder: see [How Djinn knows a command again](#gates-how-djinn-knows-a-command-again).
- **Nothing slips by.** A worker's command that goes through no gate is admitted all the same: see below.

## How a gate is granted

A command asks for a gate by its name, and waits until all of these hold:

1. **No one holds that gate alone.** A heavy command holds its name alone, for the whole machine, whatever the
   project; a light command shares it with the other light ones, as many at once as the machine holds. Heavy or light
   comes from what the command was measured to cost (see Heavy commands take turns, below), not from the gate's name.
   A command never measured counts as light. A lit ifrit holds its gates alone whatever its server costs.
2. **The machine is not under pressure.** See [Machine and load](#machine-and-load).
3. **The memory is there.** A command Djinn has measured before waits until the free memory covers its peak, with a
   margin: its usual peak, the 95th percentile of its last 20 runs, so that a run measured wrong fades after a few
   others; the memory the commands already holding gates may still take is counted as used. A command never measured
   waits for what the commands of the same kind cost (see below); with none measured, it goes at once. Three bounds
   keep a peak learned wrong from holding a worker:
   - **Alone, it goes.** When no measured command holds a gate or an admission, a command goes whatever its peak:
     waiting would free nothing for it.
   - **Half the machine at most.** A peak counts for half of the machine's memory at most: it never asks more than the
     machine has.
   - **Five minutes at most.** A command waits for the memory five minutes at most; past them it is admitted all the
     same, and its worker reads why beside its result.

   On a Mac, macOS's memory pressure decides instead of the free memory: macOS compresses, then swaps, rather than
   refuse, and always shows little free. At normal pressure a command goes, whatever its peak; heavy commands still
   take turns. At warning, a light command goes, and a heavy one waits, beside a measured holder only and five minutes
   at most, as for the memory; its wait says the level. At critical, every command waits. A peak there is the most
   memory the largest process of the command held, the compressed part included, as Activity Monitor's Memory column
   counts it: it says which commands are heavy, and the free memory Activity Monitor leaves out of its Memory Used
   stays what the window shows.
4. **Its turn has come:**
   - first, Djinn's own merges and pushes, in the order they asked: the checks of a task's merge, of a merge of main,
     of a push, and the code generation and setup they need;
   - then everyone else, the workers, the Djinn, your terminal, by the rank of the active wishes you set in the window:
     the worker of the wish ranked 1 first, then 2, then 3; within a wish the work of the first [azima](#azima) by
     rank; then the first to ask. The work of a paused wish, and a command outside any wish, comes after the ranked
     wishes, the first to ask first. The rank is read each time a gate frees: moving a wish counts from the next turn.

A merge or a push that waits for a gate keeps it from the others, even when a lighter command would fit. A gate already
held is never taken back for it, nor for a better ranked wish: Djinn waits for the holder to finish.

While a command waits, it says why: *held by W12 (Export as PDF)*, with the command W12 runs and for how long, and
*then 1 merge or push goes first* when one does; or *the machine is under pressure*, and why.

The gate goes back when the command ends, fails or is interrupted, or when its holder's process or connection ends.
**A gate is never taken back from a command that still runs**, however long it takes; only a time limit set by whoever
asked for the gate, or a lit ifrit going out, takes it back. When a command holds its gate past 3 times its usual hold
(the 95th percentile of its measured holds, and never before 10 minutes), or past an hour when it was never measured,
the lead of its wish is told once, and its task's events say it: the lead sees whether it hangs. Nothing is taken
back.

A lit [ifrit](#ifrit) keeps its gates as long as it is lit: they go back when it goes out, at its out time at the
latest, which each of your actions in Djinn pushes back, or when it ends. It goes before no one: the first wish first,
then the first come. Those who wait read *held by ifrit W12: task run-back; released when you leave, at 15:42 at the
latest*.

## How Djinn knows a command again

Djinn keeps each command's cost under a key, in its project: the same command, run by another task, in another
worktree, with other values in its environment, finds the cost measured before. The key is the command as a shell
would run it, made alike from one run to the next:

- **The folders that change with each task are named alike:** the folder it runs in, a worktree of Djinn, a scratch
  folder, a file under the temporary folder, any other identifier.
- **A variable set for the command keeps its name,** not its value.
- **A command run through a shell** (sh, bash, zsh with -c) is the command it runs.
- **What only sets up the shell is left out:** changing folder, exporting a variable, printing a line; so are the
  wrappers that change nothing it costs (time, nohup, env, a timeout), the redirections, and the filters a pipe ends
  with, such as keeping the last 50 lines.
- **A script given inline,** in a here-document, is part of its command: two inline scripts are two commands, with two
  costs, and one run again finds its cost. A file written that way before the command that runs it is left out, and
  what runs after it stays.

Arguments stay: the tests of a whole module and the tests of one package are two commands, with two costs.

**A cost learned wrong can be forgotten.** Djinn's command line forgets, in a project, the cost of one command, of the
commands that start with some words (every command starting with cat, say), or of all its commands; each forgetting is
kept in Djinn's journal, with who asked it. The next run of each command is measured anew. A wrong cost also fades by
itself: the gates hold a command to its usual peak over its last 20 runs, never to the most it ever used.

A command never measured is given what the others of the same head cost in its project, their median: the head is the
program and its subcommand, such as go test, npm run build or npx playwright test. A command of several commands, a
format then a build joined by a semicolon, or one whose program only reads or edits, such as moving or editing a file in
place, has no head: it neither lends its cost to another nor borrows one. With no such command measured, it has no cost
yet, and goes as soon as its gate is free. A cost kept before the keys were made alike is still found.

## Admitted commands

A Claude worker does not have to name a gate. Before each command of its terminal and of its watches, Djinn looks at
it:

- **A command that reads or edits** goes at once: listing and reading files, searching them, editing and moving them,
  writing a file from a here-document, git's status, log and diff, Djinn's own commands.
- **Any other** (tests, builds, package managers, scripts, interpreters, loops) waits for its **admission**, then
  runs exactly as the worker wrote it: its permissions judge it as before.

An admission is shared: many commands hold one at once. It waits only while the machine is under pressure or lacks
the memory the command peaked at, five minutes at most for the memory, in the same order as the gates: the first wish first. Once admitted, Djinn finds the
command among the worker's processes, reads what it uses, and records its cost when it ends. A command too short to be
found in a few seconds is counted asked, not measured.

**Heavy commands take turns.** A command measured past 2 GiB of memory, or past a minute of CPU time, is heavy: it
waits while another task's heavy command runs, whether that one came through a gate or an admission, and whatever the
gates' names. Under a gate's name, it holds that name alone: a light command of the same name then waits for it, while
it does not wait for the light ones already sharing the name. Djinn's merges and pushes keep their place before the workers' heavy commands. A task's own heavy
commands never wait for each other: one may run the other. The window's settings file sets both limits.

When a command waited, its worker reads why beside its result: *waited 2m for its admission by the gates: the machine
is under pressure*. Its task's events say each admission asked, taken and given back, under the name commands.

A worker whose command waits for its admission, or runs admitted, is neither paused to make room nor judged silent;
you may still pause it yourself: an admitted command keeps no one else waiting. An admission takes no slot of the
machine, unless its command is heavy and held outside a running worker. Admissions are not listed among the gates,
and the Hourglass draws only those that waited or were heavy.

## The gates Djinn runs through

Djinn's own commands each go through a gate:

| Gate | What runs through it |
| --- | --- |
| The check's own name, such as lint or test | Each check the project's settings name, before a commit or a push. |
| setup | The command that makes a fresh worktree ready, such as installing dependencies. |
| gen | Code generation, when a merge conflicts only in generated files. |
| install | The install of a build Djinn pushed. |

Each worker that edits a project is told its commit checks, and how to run them through their gates, before it ends.
It is told not to run the push checks: Djinn runs them before pushing.

## Naming gates: lint, test, stack, paid

A gate is only a name: Djinn treats every name alike. What a gate guards is what you run through it, and the names are
yours to choose. A few are common:

| Name | What it guards |
| --- | --- |
| lint, test | The checks of the same names: two heavy test suites never run at once. |
| codegen | Code generation: two generators writing the same files would step on each other. |
| e2e | An end-to-end run: a heavy browser suite runs alone. |
| stack | The local stack, a database or a development server several workers would share. An [ifrit](#ifrit) serving it can hold the gate for as long as it runs, and gives it back when it goes out. |
| paid | A run that calls a paid model or service, to find in the Hourglass what it cost. |

The name is what you read and what light commands share; whether a command holds it alone comes from what it was
measured to cost, whatever its name. Two light stacks, or two light paid runs, may run at once. A Claude worker's command that goes through no gate is admitted all the
same (see above); a command of another agent, or of your terminal, that goes through no gate is not held back by any.
See [Project and settings](#project-and-settings).

## Gates a project declares

A project can declare its own gates in its .agents/djinn.yaml, beside the ceilings of its containers: a developer who
clones it gets the same gates with nothing to set. Djinn reads the file on the project's reference branch at the last
fetch, never in a task's folder, so a worker never changes what bounds it; a change counts from the next command.

| Field | What it says |
| --- | --- |
| name | The gate's name, in lower case: neither commands nor the name of one of the project's checks. 8 gates at most. |
| alone | The commands that rebuild what the workers share (the stack, its images): they hold the gate alone, whatever they cost. |
| beside | The commands that use it: they share the gate, never while one holds it alone. |

A pattern is words, compared word by word with the start of each part of a command (cut at and, or, semicolons, pipes
and new lines), past the variables it sets and the wrappers env, time and nohup; a star stands for any words, as in
docker compose star up. No regular expression and no shell: nothing runs. 32 patterns per gate at most.

- **The gate is the project's.** It goes by name and project, stack@dolmen: another project's stack is another gate.
  Typing djinn gate run stack in the project, or an ifrit serving with the gate stack, takes the same one.
- **Alone is truly alone.** A command that holds a declared gate alone waits for those that share it, and one that
  comes to share it after it goes behind it: it never starves. A task's own commands that share it keep none of its
  own waiting: one may run the other.
- **The project only tightens.** A declared gate adds waits between the project's commands; the machine's pressure,
  its memory, the turn of the heavy commands and Djinn's merges first hold all the same.
- **The worker reads which gate.** The reason it waits names the gate, its project and its file: stack (dolmen,
  .agents/djinn.yaml) held by W81.
- **A mistake costs one gate.** A gate that is not valid is left out on its own, and the project's view says which and
  why; the other gates and the containers apply. A field Djinn does not know is ignored and said. A pattern of one word
  is kept, and said: it takes every command of that program.
- **The admissions can be turned off.** admit: false, at the top of the file, has the project's workers' commands wait
  for no admission: no Claude worker runs the gates' hook, no other agent's heavy command waits on Djinn's wrappers.
  A gate the file declares is then taken only by a command run through it by name; the checks of Djinn's merges and
  pushes still go through theirs. It counts from the next worker that starts.
- **You can turn one off on your machine.** djinn project gates shows the gates as Djinn applies them; djinn project
  gates with off and the gate's name turns it off in your own settings file, never the repository's; on turns it back.

## Slots and gates

A worker's own gate is in its slot. A gate held alone outside a running worker, by your terminal, the Djinn, a merge or
a push, takes one of the machine's slots while it is held; a gate shared by light commands takes none.

On a machine with more than one slot, Djinn keeps one free for merges and pushes: a new worker waits rather than take
it, saying a slot is kept for merges and pushes. On a machine of one slot, only the order above applies.

A worker that holds a gate alone, or waits for one, cannot be paused: Djinn says it holds the gate, to wait or stop it
instead. One whose light command only shares a gate's name, a lint beside others, can: no one waits for a gate held
shared.

## Where you see them

- **The status bar** counts the gates held: **2 gates held**.
- **A task's card** counts the time its worker spent **waiting gate**.
- **A task's events** say each gate it asked for, waited for and why, took, and gave back.
- **The Hourglass**, on its **Gates** tab, shows who holds each gate now and who waits; draws, over the last hour, 6
  hours or day, one lane per gate under the workers that run, each request in its task's colour, hatched while it waits
  for its gate; follows the named gates' requests from their task's kind to their gate to their wait; and lists each
  command admitted under a gate, the most total time first, with what it cost and how long it waited. It stays light
  under load: its figures and the waits for each gate come summed over every command, the commands come 25 to a page,
  and a command's recent runs only when you open its line (20, then all on a click). Past six free gates and eight
  gates' waits, the rest folds behind a link. After its first read, the tab asks the history only for what changed.
- **The project's configuration** lists the checks with how each last ran: on which commit, when, in how long, or why
  it failed.

Djinn's command line lists, per command, what it cost: CPU time, peak memory, duration; a page at a time, the most
total time first, with or without each command's runs, and the summary of the gates over them all when you ask. Its
history of the gates gives the last hour of holds and runs, or the minutes you name, up to a week, or only what changed
after a time you name.
Its list of gates marks a lit ifrit's gates with the time they go back at the latest, and the Djinn's brief says who
waits on an ifrit, so that the Djinn offers you to end it.

> **Example.** W4 ends while W7 lints its own work, and W12 runs the heavy test suite for a third task. The lint is
> light: W4's merge takes the gate lint at once, beside W7's, and passes. Then a push is due: its heavy gate test
> waits for W12's run to end, and goes before the next worker's heavy run.

## When Djinn restarts

Gates live in the running Djinn. A restart gives them all back: a command run through a gate goes on, saying its gate
is lost, and the queue starts again from the commands that ask anew.

Their history does not go: each gate and each admission held is kept in Djinn's database as it goes back, **for
three months**, and the Hourglass reads it again after a restart. It keeps when the holder asked, got and gave back the gate,
whether it was a named gate or an admission, whether its command was heavy or never measured, what it waited for last
and why, the most memory its holder used, and whether it took a slot outside a worker.

The history of the gates reads it back over the window you ask, an hour by default, 6 hours or a day: each hold with
the kind and code of its task, beside the runs of the workers over the same window. A run left open counts only while
its task runs: an ended task that kept a run never closed does not draw a worker running ever since.

## Good to know

- **A gate's name does not keep light commands apart.** Two light stacks may start on the same ports, two light paid
  runs may spend at once: only a command measured heavy, or a lit ifrit, holds its gate alone. Outside a gate and an
  admission, a command does not wait for anyone, and nobody waits for it.
- **Gate names are machine-wide,** unless the project declares them: the gate test of one project and the gate test of
  another are the same gate.
- **A heavy gate from your terminal takes a worker's slot.** Hold it for what you need, not longer.
- **Keep commit checks fast.** They run on every task's merge, through their gates, before anything else may: a long
  one slows every wish. Put the long suite before the push.
- **What escapes the gates meets the memory brake.** A process a command left running, or the sum of the workers, can
  still fill the memory: under 5 % available, the brake freezes the worker that grows outside the gates, never a command
  they admitted. Its percent is chosen in the Summoning Circle ([Machine and load](#machine-and-load)).
- **A long command keeps its gate.** Past 3 times its usual hold, or an hour unmeasured, its lead hears of it; the gate
  goes back only when the command ends. A lit ifrit's gates last as long as it is lit.

## Read next

- [Machine and load](#machine-and-load): how much of the machine Djinn takes.
- [Merges and corrections](#merges-and-corrections): the commit checks at work.
- [Push](#push): the push checks at work.

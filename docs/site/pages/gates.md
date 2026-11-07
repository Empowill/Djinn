# Gates

A gate is a named turn on the machine for one heavy command: the tests, a build, code generation, an end-to-end run.
Workers, Djinn's merges and pushes, the Djinn and your own terminal all run their heavy commands through the gate of
the command's name. A gate has **one holder at a time**, and is granted only while the machine can take it.

Besides the gates, every other command a Claude worker runs that neither reads nor edits is **admitted**: it waits
only while the machine cannot take it, then runs beside the others. Djinn measures each one, and a command measured
heavy takes its turn, one at a time.

## Why gates

Workers share one machine. Five workers starting the whole test suite at the same minute would each run five times
slower, run out of memory, or fail on a port another one holds. Gates turn that crowd into a queue:

- **One at a time per gate.** Two test suites never run side by side; a lint and a test suite may.
- **Only when the machine can take it.** A gate waits while the machine is under pressure, and until it has the memory
  the command needed last time.
- **The most urgent first.** Djinn's merges and pushes go first, then the first wish's commands, the work of its first
  azima first.
- **Measured.** Djinn records what each command cost, and knows next time what it needs.
- **Nothing slips by.** A worker's command that goes through no gate is admitted all the same: see below.

## How a gate is granted

A command asks for a gate by its name, and waits until all of these hold:

1. **No one holds that gate.** One holder per name, for the whole machine, whatever the project.
2. **The machine is not under pressure.** See [Machine and load](#machine-and-load).
3. **The memory is there.** A command Djinn has measured before waits until the free memory covers the most it ever
   used, with a margin; the memory the commands already holding gates may still take is counted as used. A command
   never measured goes at once.
4. **Its turn has come:**
   - first, Djinn's own merges and pushes, in the order they asked: the checks of a task's merge, of a merge of main,
     of a push, and the code generation and setup they need;
   - then everyone else, the workers, the Djinn, your terminal, the first wish by rank first, then within it the work
     of the first [azima](#azima) by rank, then the first to ask.

A merge or a push that waits for a gate keeps it from the others, even when a lighter command would fit. A gate already
held is never taken back for it: Djinn waits for the holder to finish.

While a command waits, it says why: *held by W12 (Export as PDF)*, with the command W12 runs and for how long, and
*then 1 merge or push goes first* when one does; or *the machine is under pressure*, and why.

The gate goes back when the command ends, fails or is interrupted, or when its holder's process ends. A command run
through a gate keeps it an hour at most: past that, Djinn takes the gate back, and the command goes on.

A lit [ifrit](#ifrit) keeps its gates as long as it is lit: they go back when it goes out, at its out time at the
latest, which each of your actions in Djinn pushes back, or when it ends. It goes before no one: the first wish first,
then the first come. Those who wait read *held by ifrit W12: task run-back; released when you leave, at 15:42 at the
latest*.

## Admitted commands

A Claude worker does not have to name a gate. Before each command of its terminal and of its watches, Djinn looks at
it:

- **A command that reads or edits** goes at once: listing and reading files, searching them, editing and moving them,
  git's status, log and diff, Djinn's own commands.
- **Any other** (tests, builds, package managers, scripts, interpreters, loops) waits for its **admission**, then
  runs exactly as the worker wrote it: its permissions judge it as before.

An admission is shared: many commands hold one at once. It waits only while the machine is under pressure or lacks
the memory the command peaked at, in the same order as the gates: the first wish first. Once admitted, Djinn finds the
command among the worker's processes, reads what it uses, and records its cost when it ends. A command too short to be
found in a few seconds is counted asked, not measured.

**Heavy commands take turns.** A command measured past 2 GiB of memory, or past a minute of CPU time, is heavy: it
waits while another task's heavy command runs, whether that one came through a gate or an admission, and whatever the
gates' names. Djinn's merges and pushes keep their place before the workers' heavy commands. A task's own heavy
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
| lint, test | The checks of the same names: two test suites never run at once. |
| codegen | Code generation: two generators writing the same files would step on each other. |
| e2e | An end-to-end run: one browser suite at a time, on one set of ports. |
| stack | The local stack, a database or a development server several workers would share. An [ifrit](#ifrit) serving it can hold the gate for as long as it runs, and gives it back when it goes out. |
| paid | A run that calls a paid model or service: one at a time, so that costs never pile up in parallel. |

The name keeps its own turn, one holder at a time, and is what you read; whether the command is heavy comes from what
it was measured to cost, whatever its name. A Claude worker's command that goes through no gate is admitted all the
same (see above); a command of another agent, or of your terminal, that goes through no gate is not held back by any.
See [Project and settings](#project-and-settings).

## Slots and gates

A worker's own gate is in its slot. A gate held outside a running worker, by your terminal, the Djinn, a merge or a
push, takes one of the machine's slots while it is held.

On a machine with more than one slot, Djinn keeps one free for merges and pushes: a new worker waits rather than take
it, saying a slot is kept for merges and pushes. On a machine of one slot, only the order above applies.

A worker that holds a gate, or waits for one, cannot be paused: Djinn says it holds the gate, to wait or stop it
instead.

## Where you see them

- **The status bar** counts the gates held: **2 gates held**.
- **A task's card** counts the time its worker spent **waiting gate**.
- **A task's events** say each gate it asked for, waited for and why, took, and gave back.
- **The Hourglass** draws the gates held over time, hatched while a command waits for one, and lists what each command
  cost and how long it waited.
- **The project's configuration** lists the checks with how each last ran: on which commit, when, in how long, or why
  it failed.

Djinn's command line lists, per command, what it cost: CPU time, peak memory, duration.
Its list of gates marks a lit ifrit's gates with the time they go back at the latest, and the Djinn's brief says who
waits on an ifrit, so that the Djinn offers you to end it.

> **Example.** W4 and W5 end within a minute, and W12 runs the test suite for a third task. W4's merge asks for the
> gate lint, takes it at once, and passes. W5's merge asks for lint next and goes before any worker that asks. Then
> a push is due: its gate test waits for W12's run to end, and goes before the next worker's test run.

## When Djinn restarts

Gates live in the running Djinn. A restart gives them all back: a command run through a gate goes on, saying its gate
is lost, and the queue starts again from the commands that ask anew.

## Good to know

- **Name a gate for what must run alone.** A Claude worker's command is admitted anyway, but only a gate keeps two
  stacks off the same ports, or two paid runs apart. Outside a gate and an admission, a command does not wait for
  anyone, and nobody waits for it.
- **Gate names are machine-wide.** The gate test of one project and the gate test of another are the same gate.
- **A gate from your terminal takes a worker's slot.** Hold it for what you need, not longer.
- **Keep commit checks fast.** They run on every task's merge, through their gates, before anything else may: a long
  one slows every wish. Put the long suite before the push.
- **Commands longer than an hour lose their gate.** They go on, but others may start beside them. A lit ifrit's gates
  are the exception: they last as long as it is lit.

## Read next

- [Machine and load](#machine-and-load): how much of the machine Djinn takes.
- [Merges and corrections](#merges-and-corrections): the commit checks at work.
- [Push](#push): the push checks at work.

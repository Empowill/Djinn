# Gates

A gate is a named turn on the machine for one heavy command: the tests, a build, code generation, an end-to-end run.
Workers, Djinn's merges and pushes, the Djinn and your own terminal all run their heavy commands through the gate of
the command's name. A gate has **one holder at a time**, and is granted only while the machine can take it.

## Why gates

Workers share one machine. Five workers starting the whole test suite at the same minute would each run five times
slower, run out of memory, or fail on a port another one holds. Gates turn that crowd into a queue:

- **One at a time per gate.** Two test suites never run side by side; a lint and a test suite may.
- **Only when the machine can take it.** A gate waits while the machine is under pressure, and until it has the memory
  the command needed last time.
- **The most urgent first.** Djinn's merges and pushes go first, then the first wish's commands.
- **Measured.** Djinn records what each command cost, and knows next time what it needs.

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
   - then everyone else, the workers, the Djinn, your terminal, the first wish by rank first, then the first to ask.

A merge or a push that waits for a gate keeps it from the others, even when a lighter command would fit. A gate already
held is never taken back for it: Djinn waits for the holder to finish.

While a command waits, it says why: *held by W12 (Export as PDF)*, with the command W12 runs and for how long, and
*then 1 merge or push goes first* when one does; or *the machine is under pressure*, and why.

The gate goes back when the command ends, fails or is interrupted, or when its holder's process ends. A command run
through a gate keeps it an hour at most: past that, Djinn takes the gate back, and the command goes on.

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

A command that goes through no gate is not held back by any. Djinn does not force a worker's heavy command through a
gate: the project's permissions and the workers' instructions do. A project that lets workers run commands only through
gates keeps every heavy one in the queue. See [Project and settings](#project-and-settings).

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
- **The project's view** lists the checks with how each last ran: on which commit, when, in how long, or why it failed.

Djinn's command line lists, per command, what it cost: CPU time, peak memory, duration.

> **Example.** W4 and W5 end within a minute, and W12 runs the test suite for a third task. W4's merge asks for the
> gate lint, takes it at once, and passes. W5's merge asks for lint next and goes before any worker that asks. Then
> a push is due: its gate test waits for W12's run to end, and goes before the next worker's test run.

## When Djinn restarts

Gates live in the running Djinn. A restart gives them all back: a command run through a gate goes on, saying its gate
is lost, and the queue starts again from the commands that ask anew.

## Good to know

- **Run heavy commands through a gate.** Outside one, a command does not wait for anyone, and nobody waits for it.
- **Gate names are machine-wide.** The gate test of one project and the gate test of another are the same gate.
- **A gate from your terminal takes a worker's slot.** Hold it for what you need, not longer.
- **Keep commit checks fast.** They run on every task's merge, through their gates, before anything else may: a long
  one slows every wish. Put the long suite before the push.
- **Commands longer than an hour lose their gate.** They go on, but others may start beside them.

## Read next

- [Machine and load](#machine-and-load): how much of the machine Djinn takes.
- [Merges and corrections](#merges-and-corrections): the commit checks at work.
- [Push](#push): the push checks at work.

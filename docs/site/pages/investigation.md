# Investigation

An investigation is a short study or audit the Djinn launches so that you and it agree fast: why the tests flake,
what a change would cost, where a design stands. It reads everything, runs what it needs, and ends in a tilasm that
lays out what it found and the choices, and in a question to you that links it. It never waits behind the work, and
its code is never merged.

## When the Djinn launches one

For a study or an audit whose answer is a decision of yours: a measure, a comparison, a review of a part of the code.
Never for what must land in the branch: a fix, a feature, or a prototype you judge by running it are work. A
prototype or a change you asked to see quickly is urgent work for your feedback instead
([Task and worker](#task-and-worker)); a design you review as a tilasm, version after version, is a
[mockup](#mockup).

## How it runs

An investigation is a worker in a worktree and a branch of its own, in a project in Git, like any task. It may read
every file, run commands, builds and tests, and write tilasms, blocks and questions. Whatever it commits stays on its
branch: Djinn never merges it, and the line that tells the Djinn it ended says so, integration none.

**Served first.** It goes before the planned work of its wish. On a full machine it takes the slot of a work task it
pauses, which resumes first once it ends, as an investigator of a question does. At most one runs at once on a machine
of three slots or fewer, two above, mockups counted, so that studies never starve the work. Red push checks on the
wish's branch do not hold it, nor a merge in conflict and its repair: it merges nothing.

**Short.** It has twenty-five minutes of work and ten dollars by default; the Djinn may give it more or less when it
launches it. At either, Djinn sends it a message asking it to publish what it has, its tilasm as it stands and its
question, then to end. One still working ten minutes later is stopped. The defaults come from the studies and mockups
of the wish that asked for investigations: most took fifteen minutes and six dollars, nearly all under twenty-five
minutes and ten dollars. Claude says what a run cost only at its end, so Djinn estimates it as it goes, from the
tokens it reports and the known prices.

## In the window

Its card and its line in the tree carry a small **Investigation** label, in violet. The Djinn's brief marks it too.
Its tilasm opens from the question it asked, in the wish's Tilasms tab.

## Good to know

- **Answer its question.** The investigation ends with it: what you choose becomes work.
- **Its branch stays.** What it tried is on its branch, if you want to look; cancelling it removes its worktree.
- **Not for a prototype.** A prototype runs code you judge, and that code should land: it is urgent work for your
  feedback. A design you only look at is a [mockup](#mockup), which shares this frame.

## Read next

- [Tilasm](#tilasm): what an investigation publishes.
- [Enlighten me](#enlighten): the investigator a question starts.
- [Mockup](#mockup): the same frame, for a design you iterate on.
- [Task and worker](#task-and-worker): urgent tasks, for a repair or your feedback.

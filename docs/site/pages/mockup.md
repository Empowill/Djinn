# Mockup

A mockup task shows you a design before anything is built. It puts the next version of a [tilasm](#tilasm), the
mockup you review, then brings you back the question that links it. You iterate on it from the question, as many
versions as its plan allows, and then you build the one you approve.

## Iterate or build

The Djinn asks a question whose options are typed: one iterates on the mockup, one builds it, one says something else.
Each typed option carries its plan ([Question and move](#question-and-move)): the iterating one is a mockup, the
building one is work.

- **Iterate.** Picking it stores no answer: the question stays open, one version further, and a mockup task starts
  at once, with your note. Your note is the brief of that version: unlike on any other option, it does not hold the
  task back for the Djinn. The task reads the plan, your note and the comments you sent on the tilasm, puts the next
  version under the same link, replies to each comment, then revises the question, which comes back to you in its
  place. Meanwhile the question waits for its mockup task, not for you, as one being investigated does.
- **Build.** Picking it is an answer: its task launches as any planned one, given the tilasm at the version you saw
  when you chose, whatever comes after. The decision says so: that version approved.
- **Something else.** An option without a plan goes to the Djinn, as always.

**The rounds.** A mockup plan says how many versions you review from the question, three by default, the first one
included. At the last one, Iterate launches nothing more: your answer and its note go to the Djinn, which reads the
versions with you and asks again.

**Your comments iterate too.** Comments you send on the tilasm with **Iterate** start the same round when an open
question's mockup option iterates on that tilasm: one mockup task reads them all, and no second path starts. While it
works, the tilasm holds its comments: you comment on its next version.

## How it runs

A mockup task is framed as an [investigation](#investigation): a worker in a worktree of its own, in a project in
Git, with twenty-five minutes of work and ten dollars by default, asked to publish what it has at either, and stopped
ten minutes later. It writes the mockup's files in its worktree and puts them as a version of the tilasm; it changes
no code of the project, and Djinn never merges its commits.

**Never held.** Since it changes no code, nothing the other workers do holds it back: not the red push checks of its
wish, nor a merge in conflict and its repair, nor the freeze of the merges while a branch is repaired, nor a write
scope. It is served first, beside the investigations and within their count, after the investigators of a question;
marked urgent for your feedback, it goes before them, after the wish's urgent repair.

**Its end.** The Djinn hears what it did, as of an investigator: the version it put and the question it revised. A
task that ended without revising leaves the question waiting for its revision, and the Djinn takes it up. One that
revised without a new version is flagged: the question would show you the same mockup again. If Djinn stops in
between, it starts the mockup task again when it comes back.

## Good to know

- **The Djinn may launch one itself**, to show you a design, linked to an open question or ending with its own.
- **A prototype is not a mockup.** Code you judge by running it should land: that is urgent work for your feedback
  ([Task and worker](#task-and-worker)).

## Read next

- [Tilasm](#tilasm): what a mockup puts, and how you comment on it.
- [Question and move](#question-and-move): typed options and their plans.
- [Investigation](#investigation): the frame a mockup task shares.

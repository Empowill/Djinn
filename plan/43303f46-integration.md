---
id: 01a12257-92a9-704b-9ef4-46c843303f46
code: T30
phase: 2
status: done
after: T07
---

# T30 · Djinn integrates finished work by itself

**Goal.** A worker's work counts once it is in the wish's branch, tested, not when its worker ends. Djinn brings it
there by itself: the lead no longer merges, regenerates, tests and installs by hand, and the person is asked only
what is theirs to decide.

**The developer's words.** "Why is it you who coordinates, merges, runs the tests and all, when the Go orchestrator
is supposed to do all that?" Today Djinn schedules, runs, resumes, gates and measures the workers; then each one's
work sits on its branch, and the lead integrates it by hand (`git merge`, a generated file regenerated, the tests
through a gate). It costs: nothing moves while the lead is away (W107–W110 waited through a session limit), a task
that waits for another starts on a branch without its work (W112 and W113 started before W111 was merged), and the
lead spends its tokens on mechanical work.

## What is decided

- **The deterministic part is Go, no model.** When a worker ends done, Djinn merges its branch into the wish's
  integration branch (`feat/wails-go` here: a setting of the wish in its project), in a worktree of its own, never in
  the person's checkout. A conflict only in generated files (a project setting: their paths, and the command that
  makes them, `go tool task gen` here) is settled by making them again. Then the project's test command (a project
  setting) runs through a gate; green, the integration branch moves on. The person's checkout of that branch, when
  clean and behind, follows by a fast-forward; otherwise Djinn says so and leaves it.
- **Judgement goes to a worker, not to the lead.** A conflict in code, or red tests, starts a worker of correction on
  the merge, with what failed, part of the same azima; its work integrates the same way. After a few attempts
  (a setting), Djinn asks the person a question.
- **Commit at once, push on a cadence** (the developer, 09–10/10/2026). Each task's work is merged and tested into the
  integration branch as soon as the task ends, alone: its dependents build on it at once. Pushing that branch to its
  remote is the orchestrator's, never an agent's (agents keep `git push` denied, `.agents/permissions.txtpb`).
  Djinn pushes **automatically by default**, checked each time a task's merge ends: when an azima ends (its last
  part is committed), or when at least **three tasks are committed locally and more than an hour has passed since
  the last push**. The count, the hour and the mode are settings of the wish: `auto` (the default) or `ask`, where a
  question "Push feat/wails-go to origin? (3 commits: …)" lets the person push in one click (Rub the lamp). A push
  refused by the remote (behind, protected) never forces: Djinn says why, and asks.
- **A task done without its work committed is not done.** W94 ended "done" with its whole change staged in its
  worktree and no commit: integration finds a branch with nothing new, or a worktree with changes, and says so (the
  task is not done: its work waits, uncommitted), rather than counting it.
- **A task waits for its dependencies to be integrated**, not only done, and its worktree starts from the integration
  branch, so it builds on their work.
- **The person decides what is theirs**: to install and restart on the new build (Djinn proposes it, with what
  changed and what to check), and the choices no worker can make.

## Done when

- [x] A task done integrates by itself: merged in its own worktree, generated files made again, tests through a gate,
  the branch moved on, the person's clean checkout fast-forwarded; each step in the task's events and status.
  (TestIntegrateADoneWorker, TestCommitEachTaskAlone, TestIntegrateGeneratedConflict, TestIntegrateCodeConflict,
  TestIntegrateRedTests, TestIntegrateLeavesADirtyCheckout, TestIntegrateFollowsACleanedCheckout,
  TestIntegrateABranchNoCheckoutHolds, TestRecoverAnIntegration)
- [x] Each task committed alone as it ends; the push automatic at an azima's end, or with three tasks committed and an
  hour since the last push, checked as each merge ends; `ask` mode by a question; a refused push never forced. Each
  push in the journal and on the wish (its head shows the last one); the build proposed after a push; a task's
  worktree removed once its work is committed, kept with changes not committed. Fake clock, bare remote in a temporary
  folder. (TestCommitEachTaskAlone, TestPushAtAnAzimasEnd, TestPushAfterThreeTasksAndAnHour, TestPushDue,
  TestCommittedSince, TestPushAskMode, TestARefusedPushIsNotForced, TestNoRemoteNoPush, TestABuildIsProposed,
  TestRemoveTheWorktreeOnceCommitted, the screens test "the wish's head says where Djinn last pushed its integration
  branch, and a push refused")
- [x] A code conflict or red tests start a correction worker, part of the same azima; after N attempts, a question.
  (TestCorrectACodeConflict, TestCorrectRedTests, TestCorrectionAttemptsThenAQuestion,
  TestAnswerAFailedIntegration, TestACorrectionWorkerThatFails)
- [x] A task waits for its dependencies to be integrated, and starts from the integration branch.
  (TestWaitsForTheCommit, TestADependentStartsFromTheCommit)
- [x] The window, the page and the brief show where each task's work stands (done, integrating, integrated,
  conflict, red), and propose installing once a batch is integrated. (TestWorkStands, TestBriefWorkStands, TestRun
  "where a task's work stands", the screens tests "the Tasks tab says where each task's work stands" and "the update
  banner proposes to install a build committed", TestABuildIsProposed, TestUpdateInstallsABuild)

## Merged by hand into feat/wails-go

Until Djinn integrates by itself, the lead merges finished branches. W129 (10/10/2026) brought four branches onto
W115–W122, in this order, each merge tested green (`go tool task lint`, `go tool task test`) before the next:

- **W123**, built on W121 beside W122: both kept. `ProjectSettings.install` took 9 (`correction_attempts` holds 8).
  Its `TaskIntegration.corrected_by` is W122's (7): a task's id, which the page, the brief and the window show by its
  code. A correction worker's worktree starts from its failure's base; any other from the wish's integration branch.
- **W124**: `Task.proof_needs` took 43 (40–42 held by integration, tilasms, correction). The brief lists the azimas
  awaiting their proof with the tilasms that explain them.
- **W125**: `Wish.description` took 15 (12–14 held by the integration branches and the commit cadence). The brief
  keeps its order, the wish before the rules, with the tilasms after what runs and waits.
- **W127**: golangci-lint and ESLint now check the code W116–W125 brought too: their findings fixed or, where the code
  means it, left with a reason.

The method counts of `internal/cli`'s tests, which broke at every merge, are now derived from the protos: every public
method is a command line with its help, every one that answers once is an MCP tool with its comment, and every one is
described in `docs/openapi.json` (TestEveryPublicMethodIsExpressible, TestMCPListTools, TestOpenAPI).

## Tests must be fast

No real sleep, fake clocks, milliseconds: a test over 1 s is a bug. Merges and conflicts on a real Git repository in
a temporary folder, with a fake test command.

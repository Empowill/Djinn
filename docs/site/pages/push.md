# Push

Pushing a wish's branch to the remote is Djinn's job, never a worker's. Djinn pushes the wish's
[integration branch](#integration-branch), or each azima's branch under the per-azima
[push strategy](#push-strategy), once the project's push checks pass on it, with your own Git credentials, and never by
force.

Each project has a **push cadence**: **Standard**, where Djinn pushes by itself at the right moments, or **On demand**,
where it pushes only when you ask.

## Why Djinn pushes, and not the workers

- **One CI run per push, not per worker.** Ten workers pushing ten branches would start ten pipelines; Djinn pushes the
  wish's branch once a batch of work is in, checked.
- **Checked before it leaves the machine.** The push checks, the long ones such as the whole test suite, run on the
  exact commit about to be pushed, while the merges go on.
- **Never forced.** A forced push can erase a teammate's work and the place of every review. Djinn never forces; when
  it cannot push without forcing, it stops and says so.
- **One door out.** In a Git project, any push a worker tries goes nowhere: Djinn alone talks to the remote.

## The two cadences

| Cadence | When Djinn pushes | When to choose it |
| --- | --- | --- |
| **Standard** | When an azima ends, or once three tasks are committed and more than an hour has passed since the last push. | Most projects: the remote follows the work without you thinking of it. |
| **On demand** | Only when you push. Djinn still merges each task's work into the branch, checked; it never pushes nor asks. | A branch you want to read before anyone sees it, a CI that costs, a remote you push to at set times. |

**An azima ends** when it is done or to validate, every part of it committed. A [watcher](#watcher) part of it does not
count: one that babysits the azima's pull request runs until the merge, and does not hold the push.

**Three tasks and an hour**: three tasks committed since the last push, and more than an hour since it, or since the
wish was made before its first push. Djinn looks after each task's work is committed, and every minute. Paused and
granted wishes are not pushed by the cadence.

**Merged safely first.** A push that is due waits while finished work is still in line for the branch, being merged, or
while a correction repairs it: no push check runs meanwhile, and Djinn pushes once all of it is in and checked. The
tasks' events say it: *a push of feat/invoice-pdf is due (the azima A2 ends); it waits for W7, W8 to merge, 15 minutes
at most*. Fifteen minutes after the push became due, it goes all the same, if its push checks pass: a stream of merges
never holds it back for good.

**Merges go on during the push checks.** The push checks run apart, in a worktree of their own, on the commit the
branch was at when they began: the work that ends meanwhile merges at once, its commit checks run, and the tasks that
wait for it start. A merge that lands before the push checks end makes them moot: Djinn neither pushes nor holds that
commit, says so in the tasks' events (*the push checks of feat/invoice-pdf at 1a2b3c4d are moot: it moved to 5e6f7a8b
meanwhile*), and checks the new tip once no merge is in line. Your own push, or one you approved, checks the new tip
at once instead, three times at most.

### Changing the cadence

Click the project in the side panel: its view has **Push cadence**, with **Standard**, *Pushes at an azima's end or
after 3 tasks and an hour*, and **On demand**, *Pushes only when you ask*. The switch writes your own settings file: it
changes the cadence for you, not for your team. A team that wants another default writes it in the repository's
settings. See [Project and settings](#project-and-settings).

On demand, the top of each wish says **pushes on demand**.

A single wish can also push sooner or later than the standard, or ask you before each push: *Push feat/invoice-pdf to
origin? (3 commits: …)*, answered **Push it** or **Not now**. Ask the Djinn for it; the window has no switch for it.

## Pushing yourself

Wherever a branch differs from its remote, the window shows a line for it: under the project in the side panel, in the
project's view, and on an azima's card under the per-azima strategy. Each line names the branch and counts the commits:
**feat/invoice-pdf ↑2 ↓1**, two to push, one on the remote the branch lacks. Its tooltip says which wish, or which azima,
it belongs to.

The push button at the end of the line, *Push feat/invoice-pdf* on hover, pushes that branch alone. It shows when the
branch has commits to push. Past three branches, the lines fold behind the first two.

**What main holds waits for no push.** A commit already in the remote's main, or in the branch an azima lands in, is
never counted to push: a branch whose merge request merged it into main, its remote copy left behind, has no line. A done
azima's branch whose every commit is in main is **landed**: its card says **Landed in main**, and the cross beside it
forgets its sync, so that Djinn lists it no more; nothing is deleted, here or on the remote, and a new commit on the
branch brings it back. The command line does the same with the project's forget-sync command, given the project and
the branch; it refuses a branch with commits to push.

Your push is a push like Djinn's: the push checks run first, it never forces, and it works in either cadence.
Nothing skips the push checks.

While a push runs, the line follows it, whoever started it, you or Djinn:

| The line shows | What happens |
| --- | --- |
| A spinner, *Push checks running* | The push checks run, through their gates. |
| A spinner, *Pushing feat/invoice-pdf to origin…* | Git pushes. |
| A check mark, a few seconds | Pushed. |
| A cross, *Push held: its push checks are red* | The checks failed: which, and the first lines of its output, on hover, with the correction Djinn started. |
| A cross, *The push failed* | The remote refused it, in Git's words. |
| **up to date** | Nothing to push: the remote holds the branch already. |

One push runs at a time; the other buttons wait meanwhile.

## Pulling yourself

A branch's line counts with **↓** the commits its remote holds that it lacks, as Git last fetched it. When it has any, a
pull button sits beside the push button, *Pull feat/invoice-pdf: bring it up to origin/feat/invoice-pdf, a fast-forward
only* on hover. It fetches the branch, then moves it to the remote's copy; your checkout of it follows when it is clean.
The line then reads as the branch now stands.

A pull never merges, never rebases and never forces. Djinn refuses it, with a cross on the line and the reason on hover,
when:

- the branch **has diverged** from the remote's: each holds commits the other lacks; align them by hand;
- **your checkout of the branch has changes not committed**: commit them or put them aside, then pull again;
- **work is being merged into it**: pull once that merge has ended.

The command line does the same with the project's pull command, given the project, and a branch to pull only that
one; without a branch, it pulls every branch Djinn pushes in the project that is behind, and says why for each one it
refused.

Djinn does this by itself before each merge into a branch: see
[When the remote moved on](#integration-branch-when-the-remote-moved-on).

## Where it pushes

Djinn pushes to the branch's upstream when it has one, else to **origin**, else to the repository's only remote. A
repository with no remote, or with several and none of them chosen, is not pushed.

Djinn runs Git in your repository, with your own credentials, your SSH agent or your credential helper. It never
prompts for them: a push that would wait for a password typed by hand fails.

Each push is in the wish's journal, and the top of the wish shows the last one: **Pushed feat/invoice-pdf to origin, 3
commits, 2 hours ago**, with the commits on hover.

## Held pushes

Before each push, Djinn runs the project's **push checks** on the branch's tip, each through the [gate](#gates) of its
name, in the wish's push worktree, beside the one it merges in. A check that already passed on that very commit, as a
commit check for instance, is not run again.

When a push check is red, the push is **held**:

- The top of the wish says **The push is held: its checks are red**, and the branch's line shows the cross, with the
  check that failed.
- New tasks of the wish in that project wait, saying the branch must be green again first: nothing more piles onto a
  red branch. Corrections, investigators and [investigations](#investigation) still start, and so do the **urgent**
  tasks the Djinn marks: the one that repairs the branch once Djinn's corrections gave up on it, and one for your
  quick feedback ([Task and worker](#task-and-worker)).
- Djinn runs the push checks again when new work is committed, or when the branch moves, even by a push of yours.
  Green, the hold lifts and the push goes on.

### Its correction

When the push checks are red twice in a row, or at once when the wish has no work left to commit in the project, Djinn
starts a correction worker: a task told to make the push checks pass without undoing what the branch did. It is the
branch's one repair: while it runs, nothing else merges into the branch, and when another correction already repairs
it, the push stays held until that one's work is in (see
[One repair at a time](#merges-and-corrections-one-repair-at-a-time)). The top of
the wish says **The push is held, corrected by W9**. Its work lands like any task's, and its success has the push checks
run again.

Past the project's correction attempts, two by default, Djinn stops and hands it to the Djinn, the move being its own:
retry a correction, drop the work, or leave it waiting. The Djinn decides, and asks you when it needs you. See
[Merges and corrections](#merges-and-corrections).

> **Example.** Three tasks of your wish are committed, and an hour has passed: a push is due. The push checks run the
> whole test suite, and one test fails. The push is held; the next run fails again, and Djinn starts W9 to correct it.
> W9 fixes the test, its work lands, the suite passes, and Djinn pushes the four commits. The wish's head reads
> **Pushed feat/invoice-pdf to origin, 4 commits, just now**.

## When the remote refuses

A remote may refuse a push: the branch is behind it, or protected. Djinn never forces, and asks you instead: *origin
refused the push of feat/invoice-pdf: … Djinn never forces a push: what should it do?*

- **Push again, without forcing**, once you have brought the remote's commits into the branch, or allowed the push.
- **Leave it**: Djinn tries again when the next push is due.

The top of the wish says **The remote refused the last push: Djinn asks you**, with Git's words on hover.

When the remote's copy of the branch holds commits the local branch lacks, Djinn brings the branch up to it when it
can, a fast-forward; when the two have diverged, Djinn neither pushes it nor merges more work into it until a person
aligns them. See [When the remote moved on](#integration-branch-when-the-remote-moved-on).

## After a push

When the project names an install command, the window proposes to install the build Djinn just pushed, and to restart
on it. A push made without its checks is never installed by itself.

## Good to know

- **Name a push check.** It is the last guard before your team sees the work: the whole test suite, the long build.
- **On demand is per person.** The switch writes your own file; your teammates keep their cadence.
- **A held push stops new work.** Look at the red check first: a correction may already be on it.
- **Never force-push a branch Djinn pushes.** Djinn would then find the remote apart from its branch, and stop.
- **Your credentials must work without a prompt.** A push that waits for a password fails.

## Read next

- [Integration branch](#integration-branch): the branch Djinn pushes.
- [Push strategy](#push-strategy): one branch per wish, or one per azima.
- [Gates](#gates): how the push checks share the machine.

# Pull and merge requests

A pull request, on GitHub, or a merge request, on GitLab, is how a wish's work reaches the project's main branch past
your team's review. Djinn opens them **as drafts**, **babysits** them, watching their checks, comments and conflicts
and waking the Djinn on each change, and leaves **the review and the merge to you**.

## Why Djinn stops at the draft

A developer answers for the work they put in front of others. Taking a request out of draft says "this is worth your
time": you say it, once you have read it, never an agent. So Djinn does the rest: it prepares the request, keeps it
green and answers what comes, and hands you the moments only you can decide.

| Djinn | You |
| --- | --- |
| Proposes the request when the work is ready, with a drafted title and description | Say yes, or not yet |
| Opens it as a draft | Review it, and mark it ready |
| Pushes the branch, which updates the request | |
| Watches its checks, comments and conflicts, and sets workers on them | |
| Tells you when it can be merged | Merge it |

Djinn never marks a request ready, approves it, merges it nor closes it.

## How a request gets opened

### Djinn proposes it

Djinn proposes a request when the work it would carry is ready, as a question in the wish:

- **Per wish**: once the wish has settled, no worker running and no work waiting to merge or failed, its integration
  branch pushed and holding commits main lacks: *Open a pull request to main?*
- **Per azima**: once every part of an azima is committed and its branch pushed: *Open a pull request for T07 to
  main?* Its card shows **PR proposed**.

The question carries the drafted title and description. **Open pull request** opens it, as a draft:

- **When one is open already** for the branch, Djinn takes that one, and says so.
- **When the project has a skill for opening requests**, one whose name speaks of opening or creating a pull or merge
  request, Djinn spawns a task, *Open the draft PR of* the branch, that opens it as that skill says, as a draft.
- **Otherwise** Djinn opens it itself with the forge's own command line, GitHub's or GitLab's, as a draft.
- **When it cannot**, the Djinn hears why, and that you must open it.

**Not yet** leaves it closed. Djinn does not propose the same request again: ask the Djinn when you want it opened.

See [Push strategy](#push-strategy) for which branches and which requests a wish ends in.

### A task opens it

A task may also open one, when its instructions say so, often through a project's skill. The branch must already be on
the remote: workers never push, Djinn does. See [Push](#push).

The Djinn is told that opening a request as a draft is an agent's job, and marking it ready is yours: when a task opens
one, it asks you at once to review it and take it out of draft, with the link, a button on the question that opens the
request.

## Babysitting

Once a task that opened a request ends done, Djinn watches the request by itself, in the same wish: a
[watcher](#watcher) starts on it, part of the task's azima, and the Djinn hears that the task opened it and which
watcher babysits it. The request is found by the link the task's opening printed, else by the task's branch, else by
the wish's branch.

Each change the watcher reports wakes the Djinn. What it reports is the project's skill's to say: a failed check, a
new comment, changes requested, a conflict, the merge. The Djinn then does what that skill says: typically, spawn a
task to fix the failed check, to make the change a reviewer asked for, or to settle the conflict, whose work Djinn
merges and pushes, which updates the request. The watcher ends when the skill says it is done, the request merged.

A request is never watched twice: when one already is, the Djinn hears which watcher babysits it.

### What babysitting needs

- **A skill of the project's own** that declares how to babysit a request. A skill used from another project does not
  count. Without one, nothing starts.
- **The project's settings** not turning babysitting off. It is on by default.
- **A task that ends done.** A task that fails starts no watcher.

The Djinn can also start a watcher by hand on a request, for a task that should be followed. See
[Skills and templates](#skills-and-templates) for how a skill declares one.

> **Example.** W5 opens a draft pull request, #12, for feat/invoice-pdf, and ends. W7 starts, babysitting it. The CI
> fails on a test: W7's report wakes the Djinn, which spawns W8 to fix it; W8's work is merged and pushed, and the
> request updates. The Djinn has asked you to review #12: you read it, take it out of draft, a teammate approves, you
> merge. W7 reports the merge and ends.

## Reviewing and merging

The request is yours from the draft on:

- **Review it on the forge**, where your team reviews: the question that asked you to review it has a button that
  opens it.
- **Mark it ready** when it is worth your team's time.
- **Merge it** when it is approved and green. Per azima, Djinn tells you when the request can be merged and its checks
  are green, *Pull request #12 for T07 is ready to merge?*, and only notes your answer: the merge is still yours.

Once merged, an azima's card shows **PR merged**, and the azima is done. Per wish, a question still proposing the
request closes by itself once main holds the branch.

The **Review** tab is another review: Djinn's own record of each task's merged work, which you mark reviewed as you
read it, before or alongside the request's review. See [Review](#review).

## Requests that come to you

A project's skill can also list the requests that need you, those assigned to you or whose review is asked of you.
Each one becomes a card in the **Inbox**, with where Djinn proposes to send it: a wish to file it in, or a new one. See
[Inbox](#inbox).

## Good to know

- **The branch must be pushed first.** A request shows what the remote holds; Djinn proposes one only once the branch
  is pushed as it is.
- **Answer Not yet only if you mean it.** Djinn does not propose that request again; ask the Djinn when the time comes.
- **Declare a babysitting skill.** Without one in the project, nothing watches the requests its tasks open.
- **Reviews keep their place.** Djinn keeps the branch up with main by merging, never by rebasing: the request's
  history, and its comments on lines, stay where they were.
- **Never force-push a request's branch.** Djinn would find the remote apart from its branch, and stop pushing it.

## Read next

- [Watcher](#watcher): what follows a request once it is open.
- [Push strategy](#push-strategy): one request per wish, or one per azima.
- [Review](#review): Djinn's own record of the merged work.

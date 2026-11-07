---
name: babysit-conflict
description: Each "mergeable - CONFLICTING" a babysit watcher says gets a resolution task in the same pass, part of the PR's azima (merge the target in, never rebase), one at a time per PR; after the worker that runs on the azima's branch, and before its planned tasks (--blocks)
type: feedback
---

Each line `mergeable: CONFLICTING` of a babysit watcher gets its resolution task in the same pass, whatever else the
same message says: a watcher message holds several paragraphs (a conflict, then comments), and each one is news.

- The task is `--part-of` the PR's azima (the babysit watcher's own: `djinn task get <watcher>` shows it). Its prompt:
  fetch, merge the PR's target branch (`gh pr view <pr> --json baseRefName`) into the azima's branch, resolve, run the
  commit checks; a merge, never a rebase, never a force-push. Djinn merges and pushes its work, which updates the PR;
  the watcher says when the PR no longer conflicts.
- One at a time per PR: a resolution already planned or running for it is the answer. So is Djinn's own correction
  of main's merge into that branch, when one is open: its work brings the target in.
- A worker already runs on the azima's branch (a running task part of the azima, `djinn task list --wish-id <wish>
  --open --short`): the resolution is spawned `--after` that worker, never beside it.
- The azima's next tasks, planned and not started, go after the resolution: spawn it `--blocks` them (`djinn task spawn
  <wish> --title 'Merge main into T43, resolve' --part-of T43 --after W680 --blocks W681,W682 --prompt-file <file>`),
  in the same step as the spawn. Placed later, `djinn task depend <task> --after <its other dependencies>,<resolution>`:
  `--after` replaces what the task waited for, so name them all.
- No planned task: the resolution starts at once.

**Why:** decision Q407 (option B), 07/11/2026. On 07/11/2026 at 02:34 UTC, W633, the babysit of PR #25, said `PR #25 ·
mergeable: CONFLICTING` as the first line of its first message, followed by a comment of the developer on a line of the
diff. The lead handled the comment only; no resolution started. The watcher says a state again only once it changes, so
the conflict stayed 11 hours without a word, until the developer saw it (W671 then merged main into T43's branch). The
developer's note on Q407: when a worker is already running on the branch, resolve the conflict after that worker, and
put the following workers on top of the resolution, if there are any. Djinn merges main into an azima's branch by itself
only when main holds a release the branch lacks (the default `merge_main` setting): a conflict with main's commits
between two releases, or with another target branch, is the lead's to settle.

**How to apply:** read every line of a watcher message, not only the first one or the one you came for. On a conflict,
read the azima's open work, then spawn the resolution as above before you handle the rest of the message. The skill
babysit-pr says the same in its steps; this note is why.

See also [[conflict-free-dag]], [[djinn-pushes]], [[push-hold-deadlock]].

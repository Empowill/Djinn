---
name: babysit-pr
description: Babysit a GitHub pull request until it is merged. A watcher wakes the lead only for what needs it (a
  failed check, all checks passing, a new comment, changes requested, a conflict, the merge); the lead has workers fix what fails, pushes once per round, and never merges
  by itself. Use when asked to babysit, follow or shepherd a pull request.
metadata:
  djinn:
    wish:
      title: "Babysit PR #{pr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/pull/|#|\bpr\s*#?)(?P<pr>\d+)'
      watch: "sh .agents/skills/babysit-pr/watch.sh {pr}"
      done_when: MERGED
    babysit:
      forge: github
      watch: "sh .agents/skills/babysit-pr/watch.sh {pr}"
      done_when: MERGED
    source:
      watch: "sh .agents/skills/babysit-pr/inbox.sh"
      summary: "The open pull requests of this repository assigned to you or that request your review, read with gh."
---

# Babysit a pull request

You lead a wish made for one pull request, or a wish one of whose tasks opened it: bring it to a merge.

## How Djinn helps

- The wish's watcher runs `sh .agents/skills/babysit-pr/watch.sh <pr>`. It spends no token: it looks at the pull
  request every minute, and each paragraph it prints wakes you. Its first line reaches your terminal:
  `Djinn: W1's watcher says: PR #12 · checks failed at 3f2a9c1: lint`.
- It wakes you only for what needs you, each once:
  - `checks failed at <commit>: lint, test`: a check that newly fails at the pull request's head commit, then one
    line per check with its job's link. The same failure seen again says nothing; failing again after a new push, or
    after a re-run, it is new. Check the commit: a failure of a commit you already pushed past is no news.
  - `all checks pass at <commit> (5)`: once per push, and again after a failure. Wait for it before you say the pull
    request is ready.
  - Each new comment, in a paragraph of its own: its author and its link on the first line, then `> ` and its first
    line, cut at 120 characters. Three sorts, all read:
    - `new comment by <login>: <link>`: a comment of the conversation;
    - `approved by <login>: <link>`, `changes requested by <login>: <link>`, `review by <login>: <link>`: a review,
      approving, requesting changes, or commenting with a body;
    - `new comment by <login> on <file>:<line>: <link>`: a comment on a line of the diff, a reply in its thread too.
    A review without a body that only carries comments on lines says nothing of its own: its comments speak. A
    review still pending, seen only by its author, says nothing. Each comment is said once, known by its link, even
    after a restart. The developer's own comments are said too: the watcher cannot tell them from yours, which share
    the same login.
  - `changes requested`: the review decision turned to `CHANGES_REQUESTED`.
  - `mergeable: CONFLICTING`: the pull request is no longer mergeable (`UNKNOWN`, GitHub still computing, says
    nothing).
  - `PR #12: gh failed, trying again: …`: a look failed (network, gh logged out); said once until a look succeeds.
  Pending checks and partial passes say nothing: no news is no failure yet. Its first look says what is already
  there (failures, all passing, changes requested, a conflict), and of the comments only the latest one: read the
  others yourself (step 1 below).
- It keeps what it said in `$DJINN_HOME/watchers/<task>/babysit-pr-<pr>`, never in the repository: after a restart of
  Djinn, or of its command when it dies, it does not say it again; but for the checks, which its first look says
  again (what it printed as a Djinn hung reached no one). Djinn does not wake you when that is the last line it told
  you.
- It is a POSIX shell script, for Linux and macOS. Windows is not supported: only the `sh` of Git for Windows could
  run it, and no test does.
- When the pull request is merged, the watcher prints `MERGED` and stops. Djinn then asks the developer whether to
  grant the wish. Never grant it yourself.

## Where such a wish comes from

`djinn wish route "babysit PR #12"`, or the inbox. The skill declares an inbox source,
`sh .agents/skills/babysit-pr/inbox.sh`: the open pull requests of this repository assigned to the developer or that
request their review, one paragraph each with its link. It runs only once the developer plugs it in on their machine
(`djinn inbox plug babysit-pr`, or "Plug in" in the inbox); a clone polls nothing by itself. It only reads.

A task of a wish that opens a pull request (`gh pr create`) gets its watcher too, in the same wish: Djinn starts it
once the task ends, from this skill's `metadata.djinn.babysit`, and tells you `W5 opened PR #12: W7 babysits it`.
`djinn task spawn <wish> "Babysit W5's PR" --babysit --after W5` does it by hand. There, `MERGED` ends the watcher;
the wish goes on with its other work.

## Each time the watcher speaks

1. Read what changed: the comment at its link, or all of them with `gh pr view <pr> --comments` (the conversation and
   the reviews) and `gh api repos/{owner}/{repo}/pulls/<pr>/comments` (the comments on lines of the diff, which
   `gh pr view` does not show); `gh pr checks <pr>`, and for a failed check `gh run view <run-id> --log-failed`.
2. Decide what to do. A failed check, a review comment to address, a conflict with the base branch: each is a task
   for a worker (`djinn task spawn`), with the failing log or the comment in its prompt. Group what touches the same
   files into one task.
3. Review each worker's diff, then commit and push once for the round, as the project's rules say.
4. Answer review comments only with what was done, in the developer's name only if they asked you to. A thread a
   commit fixes waits for that commit on the pull request: once its head (`gh pr view <pr> --json headRefOid`, after
   `git fetch`) contains it (`git merge-base --is-ancestor <commit> <head>`), answer citing the commit, then resolve
   the thread; never before, or an auto-merge takes the request without the fix. A thread answered with no fix may
   resolve at once.
5. Nothing to do (`all checks pass` while a review is awaited): wait for the next line. A comment that needs the
   developer is a question in the wish (`djinn question ask`); say nothing in your terminal. Never poll the checks yourself: the watcher speaks when they fail or all pass.

## Never

- Merge, close, or approve the pull request; force-push over someone else's commits.
- Work on another request here: hand it over with `djinn wish route`.

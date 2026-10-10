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
    source:
      watch: "sh .agents/skills/babysit-pr/inbox.sh"
---

# Babysit a pull request

You lead a wish made for one pull request: bring it to a merge, and nothing else.

## How Djinn helps

- The wish's watcher runs `sh .agents/skills/babysit-pr/watch.sh <pr>`. It spends no token: it looks at the pull
  request every minute, and each paragraph it prints wakes you. Its first line reaches your terminal:
  `Djinn: W1's watcher says: PR #12 · checks failed: lint`.
- It wakes you only for what needs you, each once:
  - `checks failed: lint, test`: a check that newly fails. The same failure seen again says nothing; failing again
    after a new push, or after a re-run, it is new.
  - `all checks pass (5)`: once per push, and again after a failure. Wait for it before you say the pull request is
    ready.
  - `new comment by <login>`, then `latest comment, by <login>: <its first line>`: a new comment, or a review with a
    body.
  - `changes requested`: the review decision turned to `CHANGES_REQUESTED`.
  - `mergeable: CONFLICTING`: the pull request is no longer mergeable (`UNKNOWN`, GitHub still computing, says
    nothing).
  - `PR #12: gh failed, trying again: …`: a look failed (network, gh logged out); said once until a look succeeds.
  Pending checks and partial passes say nothing: no news is no failure yet. Its first look says what is already
  there (failures, all passing, the latest comment, changes requested, a conflict).
- It keeps what it said in `$DJINN_HOME/watchers/<task>/babysit-pr-<pr>`, never in the repository: after a restart of
  Djinn it does not say it again.
- It is a POSIX shell script, for Linux and macOS. Windows is not supported: only the `sh` of Git for Windows could
  run it, and no test does.
- When the pull request is merged, the watcher prints `MERGED` and stops. Djinn then asks the developer whether to
  grant the wish. Never grant it yourself.

## Where such a wish comes from

`djinn wish route "babysit PR #12"`, or the inbox. The skill declares an inbox source,
`sh .agents/skills/babysit-pr/inbox.sh`: the open pull requests of this repository assigned to the developer or that
request their review, one paragraph each with its link. It runs only once the developer plugs it in on their machine
(`djinn inbox plug babysit-pr`, or "Plug in" in the inbox); a clone polls nothing by itself. It only reads.

## Each time the watcher speaks

1. Read what changed: `gh pr view <pr> --comments`, `gh pr checks <pr>`, and for a failed check
   `gh run view <run-id> --log-failed`.
2. Decide what to do. A failed check, a review comment to address, a conflict with the base branch: each is a task
   for a worker (`djinn task spawn`), with the failing log or the comment in its prompt. Group what touches the same
   files into one task.
3. Review each worker's diff, then commit and push once for the round, as the project's rules say.
4. Answer review comments only with what was done, in the developer's name only if they asked you to.
5. Nothing to do (a comment that needs the developer, `all checks pass` while a review is awaited): wait for the
   next line. Never poll the checks yourself: the watcher speaks when they fail or all pass.

## Never

- Merge, close, or approve the pull request; force-push over someone else's commits.
- Work on another request here: hand it over with `djinn wish route`.

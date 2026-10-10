---
name: babysit-mr
description: Babysit a GitLab merge request until it is merged. A watcher reports each change of its pipeline's jobs,
  merge status and comments; the lead has workers fix what fails, pushes once per round, and never merges by itself.
  Use when asked to babysit, follow or shepherd a merge request.
metadata:
  djinn:
    wish:
      title: "Babysit !{mr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/merge_requests/|!)(?P<mr>\d+)'
      watch: "sh .agents/skills/babysit-mr/watch.sh {mr}"
      done_when: MERGED
---

# Babysit a merge request

You lead a wish made for one merge request: bring it to a merge, and nothing else.

## How Djinn helps

- The wish's watcher runs `sh .agents/skills/babysit-mr/watch.sh <mr>`. It spends no token: it looks at the merge
  request every minute and prints a paragraph only when something changes. Its first line reaches your terminal:
  `Djinn: W1's watcher says: MR !12 · pipeline: 1 failed, 4 success (failed: lint) · merge status: ci_must_pass · …`.
- When the merge request is merged, the watcher prints `MERGED` and stops. Djinn then asks the developer whether to
  grant the wish. Never grant it yourself.

## Each time the watcher speaks

1. Read what changed: `glab mr view <mr> --comments`, `glab ci get --pipeline-id <id> --with-job-details`, and for a
   failed job `glab ci trace <job-id>`.
2. Decide what to do. A failed job, a thread to address, a conflict with the target branch: each is a task for a
   worker (`djinn task spawn`), with the failing log or the comment in its prompt. Group what touches the same files
   into one task.
3. Review each worker's diff, then commit and push once for the round, as the project's rules say.
4. Answer threads only with what was done, in the developer's name only if they asked you to.
5. Nothing to do (a pipeline still running, a thread that needs the developer, an approval to wait for): wait for the
   next line.

## Never

- Merge, close, or approve the merge request; resolve someone else's thread; force-push over someone else's commits.
- Work on another request here: hand it over with `djinn wish route`.

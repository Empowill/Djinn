---
name: open-pr
description: Open a pull request on this repository (GitHub, gh), always as a draft, its description from
  .github/pull_request_template.md, then hand its link to the lead so the developer reviews it. Never marks it ready,
  never merges. Use when asked to open, create or propose a pull request, or to ship a branch for review.
---

# Open a pull request

A developer answers for the work they put in front of others. Taking a pull request out of draft says "this is worth
your time": the developer says it, after reading it, never an agent. You make that reading quick, then hand it over.

## Branch

No ticket ids: a branch is named for what it brings, lowercase words joined by dashes.

- The head is a branch already on GitHub. Workers never push ([no worker pushes](../../../docs/providers.md#no-worker-pushes)):
  it is the branch Djinn pushes, the wish's own (`feat/<slug>`, named by `djinn wish set-integration <wish> --branch`)
  or an azima's (`djinn/<code>-<slug>`). Never a task's `w123-…` branch: none is pushed.
- Not on GitHub yet, or GitHub's is behind (`git fetch origin && git status -sb`): stop, and say so. The push is
  Djinn's or the developer's.
- The base is `main`. Another open pull request from the same head (`gh pr list --head <branch>`): update it with
  `gh pr edit`, do not open a second.

## Title

Like a commit here: `<area>: what it changes`, one plain sentence, lowercase, no period, under 72 characters
(`harness: a project naming no check integrates into a wish's own branch`). The area is `ui`, `harness`, `plan`,
`cli`, `docs`, `ci`…; a release reads `Release v0.1.1: …`.

## Always a draft

Every pull request opens as a draft (`--draft`). Then never `gh pr ready`, `gh pr merge`, `gh pr review --approve` or
`gh pr close`: those moves are the developer's.

## Description

Fill each section of [`.github/pull_request_template.md`](../../../.github/pull_request_template.md): why, what
changes, what to read first, test plan, risks. `gh pr create --body-file` skips GitHub's template: copy its sections
into your file and answer every one, "none" said plainly rather than left out.

- Name the wishes, tasks (W123) and questions (Q45) the work comes from: they hold the reasons.
- What to read first: for a large diff, the files that carry the thinking, and what is generated (`gen/**`) or moved.
- Test plan: what ran on the head and its result (the commit and push checks, an e2e spec), what to try by hand.

## Open it

```sh
git diff origin/main...origin/<branch> --stat   # re-read what you put up for review
gh pr create --draft --base main --head <branch> --title "<area>: what it changes" --body-file <file>
```

It prints the pull request's link. To fix the title or body later: `gh pr edit <pr> --title … --body-file …`.

## After

- Hand the link to the lead. A worker ends its task with the link on its last message's first line; Djinn tells the
  lead the task ended.
- The lead asks the developer to review it, a move only they can make, with the link in the question:
  `djinn question ask "Review the draft PR #12 and mark it ready: <link>" <wish-id> --move --context "<why, risks>"`.
- Djinn watches it once the task that opened it ends: the project's babysit (`metadata.djinn.babysit` of
  [`babysit-pr`](../babysit-pr/SKILL.md)) starts a watcher in the same wish, and the lead hears `W5 opened PR #12:
  W7 babysits it`. Do not start one yourself.

## Never

- Mark it ready, merge, approve or close it; push, or force-push.
- Put a ticket id in its branch, title or description.

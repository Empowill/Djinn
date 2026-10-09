# Wish templates

Some requests come back again and again: babysit a pull request, run the QA of a feature, empty a queue of
tickets. A wish template makes each one a wish that already knows how to work. It is a skill with a few more
lines: nothing to code per template.

## What happens

1. A lead hands a request over: `djinn wish route "babysit https://github.com/acme/lamp/pull/12" --wish-id <wish>
   --ask` ([routing](../plan/e1210c5e-request-routing.md)).
2. Djinn reads the templates of the skills the new wish's projects use, their own and those they summon, in the
   projects' order then by name. The first one whose `match` matches the request makes the new wish: its title
   comes from the template, and the card says which skill: "New wish “Babysit PR #12”, in lamp, from the skill
   babysit-pr". A wish on the same piece of work (its title names the same `#12` or `!12`) still takes the request.
3. You rub the lamp. The wish is made with `Wish.template`, its watcher starts at once in the skill's project
   (a [watcher](providers.md#watch-a-command-no-agent): a command, no model, no token), and its lead starts on the
   request, told to follow the skill. The wish's brief says it too, for a lead resumed later.
4. Each change the watcher prints wakes the lead. When it prints the `done_when` line, Djinn asks "W1's watcher says
   “MERGED: PR #12 is merged.”: grant “Babysit PR #12”?". A grants the wish, B keeps it open. Djinn never grants a
   wish itself.

## Declare one

In the skill's `SKILL.md`, under `metadata.djinn.wish` (Agent Skills lets a skill hold extra metadata; other tools
ignore Djinn's key):

```yaml
---
name: babysit-pr
description: Babysit a GitHub pull request until it is merged.
metadata:
  djinn:
    wish:
      title: "Babysit PR #{pr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/pull/|#|\bpr\s*#?)(?P<pr>\d+)'
      watch: "sh .agents/skills/babysit-pr/watch.sh {pr}"
      done_when: MERGED
---
```

- `title` (required): the new wish's title. `{name}` is replaced by the group `name` of `match`.
- `match` (required): a regular expression ([Go's syntax](https://pkg.go.dev/regexp/syntax)) on the request. It
  picks the template, and its named groups `(?P<name>…)` fill the placeholders. Add `(?i)` to ignore case.
- `watch`: the watcher's command line, run in the folder of the project that holds or summons the skill, without a
  shell. A placeholder's value is always one word of it, quoted when needed: a request never adds an argument.
- `done_when`: a line of the watcher that means the work is done: a line that is it, or starts with it before a
  space or a sign (`MERGED`, `MERGED: PR #12 is merged.`; not `MERGEDX`).
- `restart`: `true` for a command that exits on each change: Djinn starts it again after each exit, and stops it
  on its done line.

`djinn skill list` shows each skill's template, or why it cannot be used: no title or match, a match that does not
compile, a placeholder that is no group of the match, a `done_when` or a `restart` without `watch`.

The watcher's command must be allowed where the project lists its commands (`.agents/permissions.txtpb`): a watcher
has no reviewer. Djinn's own repository lists `sh .agents/skills/babysit-pr/watch.sh`.

## Djinn's own: babysit-pr, for GitHub

[`.agents/skills/babysit-pr/`](../.agents/skills/babysit-pr/SKILL.md) babysits a GitHub pull request. Its
`watch.sh` needs `gh`, logged in. Every minute it reads the checks (`gh pr checks`), the mergeable state, the review
decision and the comments (`gh pr view --json`), and prints a paragraph only when something changed:

```
PR #12 · checks: 1 fail, 4 pass (failed: lint) · mergeable: MERGEABLE · review: REVIEW_REQUIRED · comments: 3
latest comment, by a-reviewer: Could this name say what it holds?
```

It prints `MERGED: PR #12 is merged.` and exits on the merge, `CLOSED: …` on a close without one. It is a POSIX
shell script: on Windows, run it with the `sh` of Git for Windows. Copy the folder into your project to use it
there, with the line in your `.agents/permissions.txtpb`.

## Another project's: a GitLab merge request

A project declares its own template in its own skill. Say its developers have a command, `mrwatch`, that waits for
the next change of a merge request and prints it, then exits; and prints `MERGED` once it is merged:

```yaml
---
name: babysit-mr
description: Babysit a GitLab merge request until it is merged.
metadata:
  djinn:
    wish:
      title: "Babysit !{mr}"
      match: '(?i)\bbabysit\w*\b.*?(?:/merge_requests/|!)(?P<mr>\d+)'
      watch: "mrwatch -mr {mr} -watch"
      restart: true
      done_when: MERGED
---
```

and, in its `.agents/permissions.txtpb`:

```
commands: "mrwatch -mr"
```

`restart: true` starts `mrwatch` again after each change it reports; its `MERGED` line ends the watcher and asks
whether to grant the wish. Another project summons the skill (`djinn skill summon app/babysit-mr --into infra`) to
use the same template: the watcher runs in infra's folder, so its command must be on the `PATH` there, not a path
inside the skill's project.

Tests: `TestReadTemplate`, `TestFillQuotes`, `TestDoneLine`, `TestRouteTemplate`, `TestTemplateWish`,
`TestTemplateWithoutWatchers`, `TestSkillListTemplate` (`internal/plan/templates_test.go`), `TestWatcherDoneLine`,
`TestWatcherFinishes` (`internal/harness/watch_test.go`), and `e2e/wish-template.spec.ts` (a fake watcher).

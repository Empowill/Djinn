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
   (a [watcher](providers.md#watch-a-command-no-agent): a command, no model, no token), and the window opens it.
   The lead is told where the request went, and the wish's brief tells it to follow the skill.
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
- `restart`: `true` for a command that exits on each change: Djinn starts it again after each exit 0 too, and
  stops it on its done line. Without it, the watcher still restarts by default: a command that dies (an error, an
  exit code other than 0) starts again in the same task, keeping its state folder; an exit 0 ends it, done
  ([supervised by the Lamplighter](providers.md#watch-a-command-no-agent)).

`djinn skill list` shows each skill's template, or why it cannot be used: no title or match, a match that does not
compile, a placeholder that is no group of the match, a `done_when` or a `restart` without `watch`.

### What a watcher may run

A watcher has no reviewer. Where its permissions list commands (the project's `.agents/permissions.txtpb`, or a wish's
allowance, which lists none without that file), its command runs when it is one of three:

1. **Listed** by `.agents/permissions.txtpb` (`commands`, a start of the command on a word boundary). Djinn's own
   repository lists `sh .agents/skills/babysit-pr/watch.sh` and `sh .agents/skills/babysit-mr/watch.sh`.
2. **Declared** by a skill of the project itself: the repository says it, so it runs, with a permissions file or
   without. Strictly the command Djinn builds from the declaration: the watcher of a wish made from the skill's
   template (`metadata.djinn.wish`), on the command the wish holds, each placeholder one word as Djinn fills it; or
   the babysit watcher Djinn starts on a request (`metadata.djinn.babysit`, by itself or with `djinn task spawn
   --babysit`), `{pr}` or `{mr}` the request's number and `{state}` its own file. A summoned skill's does not count,
   nor a command typed by hand (`--provider watch --prompt '…'`), even one that starts the same way.
3. **Allowed by you** in the project, for yourself: `djinn project allow <project> --watch '<command or its start>'`,
   or the project's panel in the window ("Watcher commands you allow"). It goes into your own settings file
   (`watch_commands`, [team settings](team-settings.md)), never into the repository; `--revoke` takes it back, and
   `djinn project allow <project>` lists them.

The project's `denied_commands` win over all three. Each judges the command that runs: `$DJINN_HOME`,
`$DJINN_TASK_ID` and `$DJINN_WISH_ID` expanded ([watch](providers.md#watch-a-command-no-agent)). The start event says
which one let the command run. A refused
watcher fails at start, and its error says what the project lacks, the commands its skills declare, and the exact
`djinn project allow` command that would let it run:

```
dolmen lists no command its workers run without review (it has no .agents/permissions.txtpb, and a wish's allowance
adds none), so it does not let them run "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2855 -watch …", and
a watcher has no reviewer. No skill of dolmen declares it: one that does, under metadata.djinn.babysit or
metadata.djinn.wish (docs/wish-templates.md), lets Djinn run the command it builds from it. Or allow it for yourself
in dolmen, in your own settings, never in the repository: djinn project allow dolmen --watch 'go run
./.agents/skills/babysit-mr-empowill/mrwatch'
```

## Djinn's own: babysit-pr, for GitHub

[`.agents/skills/babysit-pr/`](../.agents/skills/babysit-pr/SKILL.md) babysits a GitHub pull request. Its
`watch.sh` needs `gh`, logged in. Every minute it reads the checks (`gh pr checks`), the mergeable state, the review
decision and the comments (`gh pr view --json`), and prints a paragraph only for what needs the lead, once each: a
check that newly fails (again after a new push), all checks passing after a push or a failure, a new comment or
review, a review decision that turns to `CHANGES_REQUESTED`, the pull request no longer mergeable. Pending checks and
partial passes say nothing:

```
PR #12 · checks failed at 3f2a9c1: lint
lint: https://github.com/o/r/actions/runs/123/job/456
PR #12 · all checks pass at 3f2a9c1 (5)
PR #12 · changes requested · new comment by a-reviewer
latest comment, by a-reviewer: Could this name say what it holds?
```

It prints `MERGED: PR #12 is merged.` and exits on the merge, `CLOSED: …` on a close without one. What it said is
kept under `$DJINN_HOME/watchers/<task>/`, never in the repository: a watcher Djinn starts again does not say it
again, but for the checks, which its first look says again; Djinn does not wake the lead when that is the task's last
line. Each paragraph names its head commit and the failed jobs: Djinn tells the lead a paragraph only when it differs
from the last one, and the same failure on a new push must not read as the old one. It is a POSIX shell script, for Linux and macOS; on Windows only the `sh` of Git for Windows could run it, and
no test does. Copy the folder into your project to use it there, with the line in your `.agents/permissions.txtpb`.

## Djinn's own: babysit-mr, for GitLab

[`.agents/skills/babysit-mr/`](../.agents/skills/babysit-mr/SKILL.md) babysits a GitLab merge request: a request with
a merge request's link (`…/-/merge_requests/12`) or its reference (`!12`) makes the wish "Babysit !12". Its
`watch.sh` needs `glab` 1.100 or later (its `--jq`), logged in, and runs in a clone of the merge request's project.
Every minute it reads the merge request, its merge status and its threads (`glab mr view --comments --output json`),
then the jobs of its latest pipeline (`glab ci get --pipeline-id`), and prints a paragraph only when something changed:

```
MR !12 · pipeline: 1 failed, 2 success, 1 warning (failed: lint) · merge status: ci_must_pass · comments: 1
latest comment, by a-reviewer: Could this name say what it holds?
```

A job that fails but is allowed to counts as a warning. The merge status is GitLab's `detailed_merge_status`
(`mergeable`, `ci_must_pass`, `not_approved`, `discussions_not_resolved`, `conflict`…); the comments leave out
GitLab's system notes. It prints `MERGED: MR !12 is merged.` and exits on the merge, `CLOSED: …` on a close without
one. It only reads: no `glab` command of it writes to GitLab. Like babysit-pr, it is a POSIX shell script; copy the
folder into your project to use it there, with the line in your `.agents/permissions.txtpb`.

## Another project's: its own watcher

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

The skill declares the command, so its template's watcher runs it without a line in `.agents/permissions.txtpb`
([what a watcher may run](#what-a-watcher-may-run)). A watcher spawned by hand on `mrwatch` needs the line:

```
commands: "mrwatch -mr"
```

`restart: true` starts `mrwatch` again after each change it reports; its `MERGED` line ends the watcher and asks
whether to grant the wish. Another project summons the skill (`djinn skill summon app/babysit-mr --into infra`) to
use the same template: the watcher runs in infra's folder, so its command must be on the `PATH` there, not a path
inside the skill's project.

## Babysitting what a task opens

A pull or merge request a task of a wish opens is watched at once, in that same wish: no new wish. A project says how
in one of its own skills, under `metadata.djinn.babysit`, next to `wish` and `source`:

```yaml
metadata:
  djinn:
    babysit:
      forge: gitlab
      watch: "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr {mr} -watch -state {state}"
      restart: true
      done_when: MERGED
```

- `watch` (required): the watcher's command line, run without a shell in the project's folder. `{pr}` and `{mr}` are
  the request's number (a pull request's number, a merge request's iid); `{state}` is a file of the watcher's own,
  `$DJINN_HOME/watchers/<task>/<skill>-<number>`, its folder made: what the command said survives a restart. One of
  `{pr}` or `{mr}` is required.
- `forge`: `github` or `gitlab`, for a project that declares one of each (Djinn's own repository does: babysit-pr
  and babysit-mr). Djinn reads the project's forge from its remote: a remote that names `gitlab` is GitLab, any other
  GitHub. The skill for its forge wins; one without `forge` serves any.
- `restart`, `done_when`: as a template's. The `done_when` line ends the watcher, done; it asks no question, since
  the wish has other work.

Only the project's own skills count: a summoned skill's watcher would run in the folder of the project that holds it.
`djinn skill list` shows each skill's babysit command, or why it cannot be used. The watcher Djinn starts runs that
command without any line in `.agents/permissions.txtpb` ([what a watcher may run](#what-a-watcher-may-run)).

**When.** Once a work task ends done, in a Git project whose skills declare a babysit, Djinn looks for the request it
opened, still open: the address its `gh pr create` or `glab mr create` printed, if the forge says it is of the
project's repository; else the one open for the task's branch; else the one open for the wish's integration branch
(`gh pr list --head`, `glab mr list --source-branch`). It spawns the watcher in the task's wish, part of the task's
azima (`Task.babysit` holds the request), and tells the lead once: `Djinn: W5 opened MR !123: W7 babysits it; follow
the skill babysit-mr-empowill each time it speaks.` A request that a task already babysits, or that the watcher of an
open wish made from a template runs on, is not watched twice. Nor is one a watcher spawned by hand already watches,
running or resuming in any wish of the project, on the declared command for that request (`{pr}` or `{mr}` filled,
`{state}` any word, both trimmed): Djinn records the request on that watcher (`Task.babysit`, its done line with it),
starts none, and tells the lead once: `Djinn: W5 opened PR #3: W2 already babysits it; …`. A look that fails (no `gh`, no network) starts nothing.

**By hand.** `djinn task spawn <wish> --title "Babysit W5's MR" --babysit --after W5` spawns the project's watcher
(its command, unless `--prompt` gives one, its restart and its done line); the request is found as it starts, the one
W5 opened, else the one open for the wish's branch. None open: the task fails, and says so.

**Off.** `babysit: false` in the project's settings ([team settings](team-settings.md)), the repository's or your own,
turns the automatic watcher off; `--babysit` still works.

## The inbox: what comes from outside

A merge request assigned to you, a mention in a thread: a skill may also declare a **source**, a command that prints
what comes from outside. Djinn runs it, and each item it prints becomes a card in the inbox, at the top of the flying
carpet, with the route Djinn proposes, as for a request handed over: a wish to file it in, or a new one, from the
skill's template when the item matches it. Nothing is made until you click; "Dismiss" sets an item aside.

**A source runs only once you plug it in, on your machine.** Declaring one in a skill runs nothing: a clone of the
repository polls no forge by itself. The empty inbox lists the sources your projects' skills declare, each with its
command and a "Plug in" button ("Unplug" once plugged in); `djinn inbox sources`, `djinn inbox plug <source>` and
`djinn inbox unplug <source>` do the same. A source is named `<project>/<skill>`, the project that holds the skill, or
by its skill alone when one project holds it. Whether it is plugged in lives in Djinn's data on this machine, never in
the repository. Unplugged, its command stops; the items it brought stay. A skill whose `metadata` is not YAML, or
whose `metadata.djinn` Djinn cannot read, is no source: its project's problems name it, in the project's panel and in
`djinn project show`, and Djinn ignores its template, source and babysit until it is fixed. One plugged in before it
broke stays listed with why, so that it can be unplugged. Another key of the front matter that breaks the YAML, like
`argument-hint: [a] [b]` (Claude Code reads it anyway), is only a warning in the same places, with its line: Djinn
reads `metadata` apart from it. Quoting the value fixes it.

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
    source:
      watch: "sh .agents/skills/babysit-mr/assigned.sh"
      every: 10m
      summary: "The merge requests of this project assigned to you, read with glab."
---
```

- `watch` (required): the command line, run without a shell in the folder of the project that holds or summons the
  skill, like a watcher. It has no placeholder: no request fills it. Where the project lists its commands
  (`.agents/permissions.txtpb`), it must be one of them.
- `every`: the least time between two starts of the command, 5 minutes by default, 1 minute at least. A command that
  waits on its own (a stream) runs as long as it likes; when it exits, Djinn starts it again `every` after its start.
- `summary`: what it reads, in a sentence, for the person who decides whether to plug it in: whose requests, where,
  read with which tool. Without one, Djinn says the skill's `description`.

**What it prints.** One item per paragraph of its output: lines separated by an empty line, or by a second of
silence. Its error output makes no item: it says why a run failed. Write each item as a request you would hand over,
its link on a line: the template's `match` then reads it as it reads a request.
For the merge requests assigned to you, `assigned.sh` could be:

```sh
glab mr list --assignee=@me --output json |
  jq -r '.[] | "Babysit !\(.iid) · \(.title), by \(.author.username)\n\(.web_url)\n"'
```

It may print every item each time: Djinn keeps the first link of an item (else its first line) and never proposes the
same one twice, whether it waits, was routed or was dismissed, nor one whose link a wish already holds in its title or
its blocks.

**What Djinn keeps of each run.** On this machine, for each source plugged in: when its command last ended, how many
items that run printed (those the inbox had already included), and, when it failed, why (its exit code and the last
lines of its error output, or why it could not start: a command the project's permissions do not list), how many runs
failed in a row and since when. A run that goes well clears the failure. `djinn inbox sources` shows it, under
`last_run`, beside the source's `summary`; unplugging the source forgets it.

**What Djinn never does.** It never writes to the source: the command's input is closed, and no answer runs a command.
It never comments, reacts, marks as read nor replies on your behalf. It stores no token: the command logs in by its own
tool (`glab auth login`, `gh auth login`, a Slack or Notion command line). It runs the sources of the skills your
projects hold or summon, and no other: one skill summoned by several projects runs once.

From the command line: `djinn inbox list` (`--all` for the routed and dismissed ones), `djinn inbox route <item>
<letter>`, `djinn inbox dismiss <item>`, `djinn inbox sources`, `djinn inbox plug <source>`, `djinn inbox unplug
<source>`. `djinn inbox sources` says what each source reads and how its last run went. `djinn skill list` shows each
source's command, or why it cannot be used.

Djinn's own `babysit-pr` declares one, `sh .agents/skills/babysit-pr/inbox.sh`: the open pull requests of the
repository assigned to you (`Babysit PR #12: …`, which proposes the skill's wish) or that request your review, each
with its link, read with `gh pr list`; its `summary` says so. Unplugged until you plug it in: `djinn inbox plug
babysit-pr`. `babysit-mr` declares no source.

Babysitting: `TestReadBabysit`, `TestProjectBabysit`, `TestFillBabysit`, `TestBabysitSettings`
(`internal/plan`), `TestBabysitWhatATaskOpens`, `TestBabysitTheTaskBranch`, `TestBabysitOffOrUndeclared`,
`TestBabysitByHand` (`internal/harness/babysit_test.go`: a fake `gh` and `glab`, no network).

Tests: `TestReadTemplate`, `TestFillQuotes`, `TestDoneLine`, `TestRouteTemplate`, `TestTemplateWish`,
`TestTemplateWithoutWatchers`, `TestSkillListTemplate` (`internal/plan/templates_test.go`), `TestWatcherDoneLine`,
`TestWatcherFinishes` (`internal/harness/watch_test.go`), `TestBabysitMRReadsOnly`
(`internal/harness/babysit_mr_test.go`: babysit-mr's template and watcher, a fake `glab` on the `PATH`),
`TestBabysitPRSpeaksOnlyForTheLead` (`internal/harness/babysit_pr_test.go`: what babysit-pr's watcher says, and does not
say again after a restart, a fake `gh` on the `PATH`), and `e2e/wish-template.spec.ts` (a fake watcher). The
inbox: `TestReadSource`, `TestSources`, `TestKeepSourceRun`, `TestOwnSource`, `TestInbox`, `TestItemKey`
(`internal/plan/inbox_test.go`), `TestSourceReadsOnly`, `TestSourceRefused`, `TestSourceFails`, `TestSourceUnplugged`
(`internal/harness/watch_source_test.go`),
`TestInboxPlug` (`cmd/djinn/inbox_test.go`), and `e2e/inbox.spec.ts` (a fake source, plugged in from the inbox).

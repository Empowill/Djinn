---
id: 01a12054-f8df-7dd1-a750-427abac5e018
code: T27
phase: 2
status: in-progress
after: T26
---

# T27 · Wish templates, drawn from skills

The design's second part ("every request finds its wish", 09/10/2026, decision Q51 = B), after the routing
([T26](e1210c5e-request-routing.md)) and the watcher (`--provider watch`). A request that comes back, such as babysitting a pull request,
opens a wish that already knows how to work. How a project declares one: [`docs/wish-templates.md`](../docs/wish-templates.md).

**What was decided.**
- **A template is a skill with a few more lines**, in its `SKILL.md` front matter under `metadata.djinn.wish`:
  `title` and `match` (required), `watch`, `done_when`, `restart`. `match` is a Go regular expression whose named
  groups fill the `{name}` placeholders; a value is one quoted word of the watcher's command line, never more.
  Read with go-yaml (`go.yaml.in/yaml/v3`, MIT and Apache-2.0, already linked through protovalidate).
- **Routing proposes it** (`plan.propose`): the templates of the new wish's projects, own skills then summoned
  ones, the first match wins. The new wish's option carries `RouteOption.template`, its title from the template,
  "from the skill …" on the card. A wish on the same piece of work still takes the request (the bar to file rises
  to `routeRef + routeWord`).
- **The wish keeps it**: `Wish.template` (`WishTemplate`: skill, project, watch filled, done_when, restart), mapped
  on import like the projects. Answering the card makes the wish, then starts its watcher in the skill's project
  (`plan.WithWatchers`, `harness.SpawnWatcher`), then its lead, whose first line names the skill's `SKILL.md` and
  the watcher's code (or why it did not start, and the command to start it). The brief says it too.
- **The done line is a card, never a grant**: each new paragraph of a watcher reaches `plan.Wishes.Watched`
  (`harness.OnWatched`); a line that is `done_when`, or starts with it before a sign, asks `Question.grant`: A grants
  the wish in the answer's transaction (journaled as `djinn wish grant`), B keeps it open. Asked once while open.
  A watcher that restarts its command ends, done, on its done line.
- **Djinn ships two**: `.agents/skills/babysit-pr/` for GitHub, its `watch.sh` on `gh pr checks` and
  `gh pr view --json`; `.agents/skills/babysit-mr/` for GitLab, its `watch.sh` on `glab mr view --comments` and
  `glab ci get` (glab 1.100 or later, for `--jq`). Both print only on change, exit on the merge, and only read.
  Djinn's `.agents/permissions.txtpb` lists both.

## Done when

- [x] A skill's template parses, or says why it cannot be used, in `djinn skill list`. (`TestReadTemplate`,
  `TestSkillListTemplate`)
- [x] A request matches a template and fills its placeholders, one quoted word each. (`TestReadTemplate`,
  `TestFillQuotes`)
- [x] Routing proposes the template's wish; the same piece of work still files. (`TestRouteTemplate`)
- [x] The wish is made with its template, its watcher started in the skill's project, its lead on the skill.
  (`TestTemplateWish`, `TestTemplateWithoutWatchers`, `TestWatcherDoneLine`)
- [x] The done line asks once whether to grant; A grants, B keeps it open; a restarting watcher ends done.
  (`TestDoneLine`, `TestTemplateWish`, `TestWatcherDoneLine`, `TestWatcherFinishes`)
- [x] E2e with a fake watcher: route, rub the lamp, the watcher's MERGED card, rub the lamp, the wish is granted.
  (`e2e/wish-template.spec.ts`)
- [x] `babysit-pr` for GitHub, and the docs of a GitLab one. (`.agents/skills/babysit-pr/`, run by hand on a public
  pull request open then merged: a summary, then `MERGED` and exit 0; `docs/wish-templates.md`)
- [x] `babysit-mr` for GitLab: its template matches a merge request's link or `!12`, not a pull request; its watcher,
  under Djinn's permissions, prints each change of the pipeline's jobs, merge status and comments, `MERGED` on the
  merge and exits, and only reads. (`TestBabysitMRReadsOnly`, a fake `glab` on the `PATH`; `docs/wish-templates.md`)
- [ ] `babysit-mr` run by hand on a real merge request open then merged. (needs: a person and a GitLab project)
- [x] The brief's rules tell a lead to spawn a watcher (`--provider watch`, `--restart`) instead of polling, and to
  propose a template (`metadata.djinn.wish`) for a request that comes back. (`TestBrief`, `briefRules`;
  `docs/agent-protocol.md`)
- [ ] A real lead babysits a real pull request of Djinn to its merge, from the template. (needs: a person and a real
  model)

## Next

- More templates: QA of a feature, its screenshots kept as blocks of the wish; a queue of tickets, one task per
  ticket, the ticket claimed when its worker starts. Each is a skill, no code. (The design, 09/10/2026.)
- The inbox, the design's third part: [T28](d6fb2417-inbox.md).

## Open questions

- Who pushes a babysat pull request's fixes? The design (09/10/2026) and both babysit skills have the lead review
  the workers' diffs, then "commit and push once for the round". Since T30, agents never push: Djinn commits each
  task's work into the wish's integration branch, by default the branch the project's checkout was on when the wish
  was made, and pushes it. For a babysit wish that branch must be the pull request's own: `djinn wish set-integration`
  can set it, but neither the template nor the skills do, and the skills' step 3 still tells the lead to push.

- The Agent Skills format describes `metadata` as a map of strings; `metadata.djinn.wish` nests a map. Claude Code
  reads it; a strict validator may not. A flat form (`djinn.wish.title: …`) would satisfy both, at the cost of
  readability. Keep the nested form until a tool refuses it?

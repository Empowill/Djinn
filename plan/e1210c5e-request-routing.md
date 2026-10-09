---
id: 01a1203c-1d89-752d-84fb-a2a1e1210c5e
code: T26
phase: 2
status: in-progress
---

# T26 · Every request finds its wish: routing

The developer's words: "When I ask you to babysit something, or anything that has nothing to do with the current wish,
offer to open a wish, dreaming a little further, or to file it in an existing one." Two babysits of merge requests
slipped into one wish and into its lead's session. Decision Q51 = B; the design is the wish's design block "every request
finds its wish". This task is its first part, the routing.

**What was decided.**
- **`WishService.Route`**, `djinn wish route "<request>" [--wish-id <wish>] [--ask] [--title …] [--project-id …]`.
  Djinn ranks the wishes without a model (`plan.propose`): a project the request links to (a GitLab or GitHub link
  whose repository is a project's remote, or the repository's path, `acme/shop`) or names, the words of the titles
  (a project's name counted once), the same merge request or issue (`!41`, `#12`, from the text or the link; another
  one weighs against), and a little for the words of the five latest blocks. A wish is proposed from 3, and
  recommended from 6; else Djinn recommends a new wish, its title made from the request (links shortened to
  `shop!41`), in the projects the request points to, else those of the wish it came to. The wish it came to and
  the granted ones are never proposed.
- **The card is a question**: `Question.route` (13) holds the `Route`, its options in the question's order, the
  recommended one first (`A: …`), so Rub the lamp takes it. The texts are the developer's language
  (`plan.WithLanguage`, `locales` `route.*`).
- **Answering acts in the same transaction** (`Questions.Settle`, `Marks.Settle`), each change journaled as the
  command that would make it: filing puts a `request` block in the wish, and tells its lead (W52's path); a new wish
  is made with its projects and the request block, its option records the new wish, and its lead starts from the
  brief in its own terminal (`lead-<wish>`), the request first (`plan.FirstLine`), shown in the window. The lead of
  the wish the request came to is told where it went (`plan.RoutedLine`). A route question answers once.
- **Three wishes at most**: when full, the card offers the new wish paused (`ROUTE_KIND_QUEUE`, no lead started) or
  to pause the last active wish but the lead's own (`ROUTE_KIND_SWAP`).
- **The brief's rules** tell the lead: a request that is not about this wish goes through
  `djinn wish route "<request>" --wish-id <wish> --ask`; hand it over, do not do its work.

## Done when

- [x] `djinn wish route` ranks the wishes and proposes where a request goes, without a model. (`TestRouteRanking`:
  a link to another merge request → a new wish in its project; the same merge request → that wish; a repository's
  path; title words and a project named, never a granted wish; the latest blocks; no match → a new wish; three
  active → queue or swap. `TestProposeTitle`)
- [x] `--ask` asks it as a question card on the current wish, the recommendation first, rubbable.
  (`TestRouteAnswered`: options, recommendation, `Recommended` = A)
- [x] Both answers act end to end with fake leads: a new wish made with its project, its request block, its lead
  started on the brief with the request first and shown, the current lead told; filing adds the block and tells
  that wish's lead; queue and swap keep three active; a second answer is refused. (`TestRouteAnswered`)
- [x] The brief's rules hand a request over through `djinn wish route --ask`. (`internal/plan/brief.go`, `briefRules`)
- [x] E2e: route a request, rub the lamp, see the new wish with its lead terminal. (`e2e/wish-route.spec.ts`)
- [ ] A real lead hands a request over by itself, from the brief's rule. (needs: a person and a real model)

## Next (the design's parts 2 and 3)

- Wish templates drawn from skills (babysit with a `watch` provider, Dew, QA): [T27](bac5e018-wish-templates.md).
- An inbox: what comes from outside (a mention, an assigned merge request) becomes a proposed wish:
  [T28](d6fb2417-inbox.md).

## Open questions

- A new lead's provider follows the lead of the wish the request came to, else claude. Should the card offer it?

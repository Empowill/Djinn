# Adaptable wishes, a simpler experience

The design decisions behind how Djinn runs a wish, its permissions, its tests and its interface.

## Intent

Djinn steers a team of agents towards a usable result as fast as possible. The user can change
priorities, ask for a fix, test a ticket or stop the work at any time. The timeline follows the
work; it is not a technical authorization system.

## Permissions

- Ask for technical permissions when they are needed; `never` is no longer the default setting.
- Show the native Codex and Claude Code requests in the wish: agent, provider, reason, exact
  action and scope.
- Accept once, refuse, or allow for the session when the provider supports it. The answer resumes
  the pending native request.
- Notify a useful intervention and open the card it concerns directly.
- A request that was cancelled, already resolved, or belongs to a stopped run cannot be accepted.
- A review can take a fix or a local launch explicitly asked for. The provider stays in charge of
  its permissions and its sandbox.

## Work and verification

- Keep the decisions already made, the human instructions and the results across resumes.
- Propose the smallest useful workflow, and limit questions to the business trade-offs that change
  the result.
- Keep sub-agents on a bounded goal, with explicit ownership of files; independent work moves
  forward separately.
- The lead stays reachable while they work; extra supervision starts only in answer to a human
  intervention. Integration keeps its role at the end.
- Send compact excerpts on each run, and make the full text documents available on demand from
  Djinn's private storage.
- Check the affected areas according to their risk. Existing evidence stays valid as long as the
  relevant scope has not changed.
- Tell apart a finished run, a ready result, a needed answer and a blocker. A finished run does not
  prove that the exit criteria are met.
- Keep human validation of testing and delivery. Reversible local actions need no extra timeline
  validation.
- Resolve the folder explicitly asked for servers, hidden or deep worktrees included, while
  keeping the path confined to the project.

## Interface

- Avatars on the right of the tab bar: the current team, lead included.
- One to three agents: all visible. Four or more: three avatars and `+X` for the others.
- Full name and status on hover or focus; clicking an avatar opens its chat. `+X` opens a picker
  in a drawer.
- Agents keep a stable order. Their details and history stay available on demand.
- "Your turn" gathers the permissions, open decisions and available tests. Ordinary updates stay
  in the journal.

## Testing

- Each action states the result to test, where it comes from, what to do and the expected
  behavior.
- Before availability is checked: prepare the test or retry, with a visible cause of failure.
- Once the server is available: open the test. The user can report that the result works,
  describe a problem, or postpone the test.
- The feedback is kept with the action and its step; a problem goes to the lead with its context.
- A notification asks for an intervention or announces a testable result; it does not repeat
  every internal progress.

## Acceptance criteria

1. A native request stays pending until the human answers, then resumes or refuses the matching
   operation without recreating the wish.
2. An expired permission or a duplicate answer is rejected; a reload never revives a permission
   with no active native request.
3. A hidden worktree with a valid script can be started directly; a path outside the project is
   refused.
4. A result explicitly blocked, or unmet criteria, never lead to an automatic test.
5. The avatars and the drawer work with the keyboard and follow the overflow rule exactly.
6. Test feedback survives a save and restore, and stays tied to its result.
7. A resume uses a compact context, favoring the sources of truth and the latest human
   instructions.

---
id: 01a12bdf-33f4-725d-8ef9-5af3c05df1a2
code: T2
phase: 1
status: open
---

# T2 · Wish creation and opening motion

**Goal.** A new wish opens through one continuous, accessible smoke transition.

## Work

- Enter creation with upward movement, fading and staggered title, options, editor and action.
- On success, finish the upward content exit, expand the existing smoke across the window, dissipate it through noise for about 800 ms, then reveal the wish with matching motion.
- Keep drafts usable after errors, prevent duplicate submissions, preserve focus and navigation, and clean up on cancellation or unmount.
- Use a concise reduced-motion alternative and deterministic browser tests with no model calls or new dependencies.
- Own only creation, app entry, action and smoke files; integrate workspace sections through a `.wish-entry` wrapper.

## Done when

- [x] Success, recovery, reduced motion and overlay cleanup pass controlled browser tests.
- [x] Interface tests and the lint gate pass.
- [ ] The sequence is visually checked in the native window (needs: a person).

## Proof

- Gated UI suite (`djinn gate run test -- go tool task test-ui`): PASS, 1s, exit 0 ([log](/tmp/codex-verification/djinn-w2-test-ui-v2.log)).
- Gated reduced-motion worker regression: PASS, 37s, exit 0 ([log](/tmp/codex-verification/djinn-w2-e2e-worker-v2.log)).
- Gated transitions, recovery, composer and navigation route E2E: PASS, 52s, exit 0 ([log](/tmp/codex-verification/djinn-w2-e2e-v3.log)).
- Capped lint gate (`GOMAXPROCS=2 GOMEMLIMIT=512MiB`, existing Djinn binary on `PATH`): PASS, 12s, exit 0 ([log](/tmp/codex-verification/djinn-w2-lint-v7.log)).
- Native visual check remains (needs: a person).

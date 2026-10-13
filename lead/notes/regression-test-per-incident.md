---
name: regression-test-per-incident
description: Every incident fix ships a regression test that reproduces it; no replay on a copy of the real Djinn (Q197 A); weigh the lead's token cost against a test
type: feedback
---

When a task fixes an incident, its prompt requires a regression test that reproduces the incident, on fake data and
fast, run with the push checks. No replay of the incident on a copy of the developer's Djinn.

**Why:** Q197 A (11/10/2026), after the developer asked whether a replay task was needed at all, "your tokens might
be more expensive than a test": a lead running a replay and reading its report spends an expensive model's tokens
every time; a test costs nothing per push.

**How to apply:** in every incident-fix prompt, name the scenario to reproduce as a test; never ask a worker to run a
replay. Weigh the lead's token cost before adding any manual step for the lead.

See also [[conflict-free-dag]].

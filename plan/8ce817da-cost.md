---
id: 01a11879-2612-7d67-ad37-0a708ce817da
code: T14
phase: 3
status: done
---

# T14 · Spend big models only where they matter

**Goal.** Delegating to Djinn costs less than doing the same work in one big session: large
models do the work that needs them (planning, hard code, review), and everything else runs on
code, cheaper models, or a local model.

## Ideas, cheapest first
- **Dispatching is code, not a model.** Scheduling, write scopes, dependencies and gates are
  deterministic: they cost no token.
- **One model per task, chosen in the task file.** A large model to plan and review; a smaller,
  cheaper one for mechanical changes.
- **Smaller prompts.** A worker gets the project index and its task file, not the whole
  conversation. Stable prefixes (`AGENTS.md`, the index) benefit from prompt caching.
- **Resume instead of restart.** Today each pass starts a new provider process and rebuilds its
  context; resuming a session keeps the cache warm.
- **A local open-weight model, for dispatch only, and only if plain code cannot do it**: see T16,
  which benchmarks Gemma 4 against the Go scheduler.
- **Measure cost per task and per worker** (tokens and money), so every choice above is checked
  against numbers.

## Done when
- [x] Each task records the tokens and the cost it used. (08/10: a fake task that reports `usage 1200 300 0.02`
  lists `usage: input_tokens 1200, output_tokens 300, cost_usd 0.02`; Claude's result is read by `TestParseClaude`;
  Codex and agy give tokens only)


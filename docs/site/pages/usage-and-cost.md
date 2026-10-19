# Usage and cost

Every task, every wish and the flying carpet show the tokens their workers used, and an estimated cost from a price
table built into Djinn; where an agent reports its own cost, it shows beside the estimate. Djinn asks for no key: the
account each agent signs in with pays for it.

## How session usage is recorded

- **Cumulative session snapshots.** Claude and Antigravity report cumulative token and cost totals for the entire
  session at the end of each turn. When a task resumes or continues on the same session, Djinn replaces that session's
  snapshot rather than adding the earlier turns a second time.
- **Switching sessions.** When a task starts a new session, for instance after switching agents, the tokens and cost
  spent on its earlier sessions are carried forward as a baseline and added to the new session's usage.
- **Forked sessions.** When a task forks an existing session, the new session begins with the parent session's
  cumulative token history; Djinn subtracts the parent task's baseline so the forked task records only the tokens and
  cost of its own turns.
- **Thinking tokens.** Reasoning and thinking tokens are recorded in the task's usage across agents, including the
  per-model thinking token counts Claude reports at the end of a turn.

## Estimated and reported costs

- **Price table.** Djinn embeds a versioned per-million-token price table covering Anthropic (Claude Fable 5.1, Opus 5.6,
  Opus 5.5, Sonnet 5.6, Haiku 5.5, with the one-hour cache write rate of headless Claude Code), OpenAI (GPT-5.4, GPT-5.4
  Mini, GPT-5.3 Codex, GPT-5.2 Codex, GPT-5.1 Codex Max), and Google Antigravity (Gemini 4 Argon High and Medium, Gemini
  3.8 Flash High and Medium).
- **Estimated when the agent reports zero.** Claude reports monetary cost directly in its stream, which Djinn preserves
  alongside the price-table estimate. For agents whose stream reports zero cost (Antigravity and Codex), Djinn fills in
  the task's cost from the price table using the task's resolved model.

## Read next

Related: [Machine and load](#machine-and-load), [Task and worker](#task-and-worker).

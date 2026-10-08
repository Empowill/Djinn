---
id: 01a1188a-d9b7-7999-a9e1-6350a72eb1f8
code: T18
phase: 2
status: in-progress
---

# T18 · Antigravity as a worker provider

**Goal.** Djinn can hand a task to Google's Antigravity CLI (`agy`), next to Claude Code and
Codex, within what Google's terms allow.

## What the CLI offers (read from `agy --help`, version 1.3.0)
- Headless mode: `agy -p "<prompt>"`, with `--output-format stream-json` and
  `--input-format stream-json` (one NDJSON message per turn), close to `claude -p`.
- `--add-dir` for the worktree, `--model`, `--conversation <id>` to resume, `--json-schema` for
  structured output, `--print-timeout`.

## How it signs in, and who pays (read 8 October 2026)
- **Gemini API keys are not the way.** Since 19 June 2026 the Gemini API refuses keys without API
  restrictions, Vertex AI does not accept API keys at all, and the Antigravity enterprise
  documentation does not support Gemini API keys.
- **Two supported ways for a team on Google Cloud** (Antigravity enterprise documentation, May
  2026):
  - **Application Default Credentials**: each developer runs
    `gcloud auth application-default login --project <project>` with their own Google identity;
    the CLI uses them when `AGY_ADC_AUTH=true` is set.
  - **Single sign-on with a license**: the developer signs in with a business account and picks
    the license, linked to a Google Cloud project.
- **The project of the selected license is billed** for model usage, at consumption pricing, and
  receives the logs of model interactions. The CLI needs the Vertex AI API
  (`aiplatform.googleapis.com`) and the `aiplatform.endpoints.predict` permission.
- **Per-developer cost is not documented by Google.** Ways to get it:
  - Djinn records what each worker consumed (tokens from the CLI's JSON output, if it reports
    them) and who started it: the cost per user and per task, whatever the provider (T14).
  - Google Cloud audit logs of Vertex AI record which identity made each call: a cross-check per
    developer.
  - One Google Cloud project per developer gives a separate bill each, at the price of more
    projects to manage.

## What the terms allow
- Spawning the **official, unmodified** `agy` binary through its **documented headless flags**,
  letting it sign in by itself: allowed according to an answer on Google's AI developer forum
  (7 September 2026), not confirmed as coming from Google. With ADC on the team's own Google Cloud
  project, the usage is the team's paid consumption, the clearest case.
- Forbidden: harvesting or reusing its sign-in tokens, proxies, calling private endpoints, going
  around usage limits (accounts suspended, announcement of 27 February 2026).

## Decided (Q20): two ways to sign in
- **First choice**: Application Default Credentials of each developer on the team's Google Cloud project
  (`gcloud auth application-default login --project <project>`, then `AGY_ADC_AUTH=true` where `djinn up`
  runs).
- **Second choice**: agy's own sign-in with a Google account (a browser, the session in the system keyring).
- Djinn never reads credential files and sets no sign-in variable itself; it only passes on what the user has
  set. The steps, one by one: [`docs/providers.md`](../docs/providers.md#signing-in). `AGY_ADC_AUTH` and
  `GOOGLE_CLOUD_PROJECT` are names found in the agy 1.3.0 binary.

## Rules for Djinn
- Run the official binary as installed; never read its configuration, its token files or the ADC
  file. Djinn may pass `AGY_ADC_AUTH=true` and the project to the process, nothing more.
- One user, one identity: a worker runs with the identity of the person whose wish it serves, so
  the cost lands on them (relevant to T15).
- Prefer Djinn's permission relay over `--dangerously-skip-permissions`. The stream-json mode exposes no
  permission prompt: a tool needing approval is soft-denied, named on the error output.

## Done so far
- `internal/harness/agy.go`: `agy --input-format stream-json --output-format stream-json`, one turn per message
  on the input (no `-p`: it takes the prompt as its value), `--conversation` to resume, `--model`. In a project
  no `--mode`: agy's settings decide (Q34); outside any project `--mode plan --sandbox`. Never
  `--dangerously-skip-permissions`.
- The stream read into Djinn's events (text deltas gathered per step, tool steps, result with its usage,
  `AGY_ERROR` and the soft-denial notice on the error output); tokens only, agy gives no cost.
- Nine cases replayed by `TestCatalog`, all **written from the documentation and the binary, none captured**:
  [`docs/providers.md`](../docs/providers.md#antigravity).

## Done when
- [x] A worker can be started with `agy` in a worktree (`djinn task spawn --provider antigravity`), its
  stream-json events shown like Claude's. Not yet run against a real model.
- [ ] A first real run, signed in by ADC, captured into fixtures replacing the supposed ones. (needs: a person
  signed in to Antigravity, and a paid run)
- [ ] Checked on a real run: what plan mode and `--sandbox` really block in headless mode, and whether settings
  allow-rules apply headless (the documentation says granted tools run; the 1.3.0 binary says they do not).
  (needs: the same real run)
- [ ] The README lists Antigravity as optional, with the rules above. (needs: an agent; the README names Codex
  and Claude Code only)

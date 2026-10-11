# How each agent wakes up when a command it left running ends

`djinn lead watch` (decision Q191) only saves the lead's turns if its agent wakes it up when the watch exits. These
fixtures are real sessions on this machine, cut to the steps that show it; `wake_test.go` reads them. No model was
called to make them: they come from sessions that had already run. Paths, ids of processes and the agents' own words
were replaced by placeholders; the fields, their order, the tool names, the parameters and the times are as recorded.

- `claude.jsonl`: Claude Code 2.1.296, an interactive lead session (`entrypoint: cli`), 2026-10-11. A Bash command run
  with `run_in_background: true`; the turn ends (`end_turn`); 39 s later the command exits and Claude Code adds a
  `<task-notification>` user message; the agent acts 4 s later, without anyone typing. Claude Code's documentation
  (tools reference, "Background commands") says an interactive session sets no time limit on background commands, and
  that `CLAUDE_CODE_SESSION_ID` is set in Bash subprocesses.
- `agy.jsonl`: Antigravity (agy) 1.3.3, a Djinn worker (headless, stream-json), 2026-10-11. A `run_command` outlives
  its `WaitMsBeforeAsync` and goes to the background as a task; the turn ends ("I will wait for the tests to finish.");
  4 minutes later a `SYSTEM_MESSAGE` says `Task id "…" finished with result`, and the model acts at once. agy's own
  tool description, in its binary, says: "Commands that do not finish within the specified wait time continue running
  in the background as tasks; the system will notify you when they finish", and `WaitMsBeforeAsync` takes 500 to 10000
  ms. The same wake-ups appear after waits of more than an hour in other runs.
- **Codex**: no fixture. Codex is not installed here, and its source (openai/codex, rust-v0.162.1, 2026-10-09) shows
  no wake-up to record: `exec_command` yields after `yield_time_ms` (30 s at most) and leaves a still running process
  to be polled with `write_stdin`; when it exits, `spawn_exit_watcher` (core/src/unified_exec/async_watcher.rs) only
  emits an `ExecCommandEnd` event for the interface, it starts no turn. So a codex lead keeps no watch running, and
  Djinn types its one line, "Djinn: N events waiting, run djinn lead watch". Codex sets `CODEX_THREAD_ID` for its
  commands, which names the lead's session.

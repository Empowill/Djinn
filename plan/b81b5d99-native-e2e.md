---
id: 01a1184f-cf1a-7ae3-a231-dabcb81b5d99
code: T06
phase: 1
status: open
---

# T06 · End-to-end tests on the native window

**Goal.** An agent drives the real native window, not only the browser.

## What to do
- Try the experimental MCP server Wails runs in dev mode (`WAILS_MCP=1`, local, behind a token):
  DOM queries, clicks, typing, bound method calls.
- Turn one browser spec into a native one, run by a task.

## Done when
- [ ] One end-to-end scenario runs against the native window on Linux and macOS.

## Open questions
- Which way to drive the real window: the MCP server Wails runs in dev mode, or a WebDriver bridge on Linux? *Recommendation: try the Wails MCP server first; it works on macOS and Linux.*

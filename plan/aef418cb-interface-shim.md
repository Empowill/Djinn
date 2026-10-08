---
id: 01a1184f-cf17-786b-a218-3485aef418cb
code: T03
phase: 1
status: open
---

# T03 · Keep the interface working: the `window.djinn` shim

**Goal.** The React interface runs unchanged inside the native window and in the browser.

## Decided
- **The interface moves to the new data all at once** (not screen by screen): it reads wishes,
  tasks, questions, events and blocks from the Go services, in the protos' own shapes. The
  shim's conversion to the old mission model is a bridge for the first imports only, removed
  when the switch lands.
- **Old data is converted once, by the person who has some**: a user with flight plans in the
  Electron format converts them with their own agent (old `djinn-session` file in, `djinn wish
  import` file out). Djinn ships no converter.

## What to do
- Provide `window.djinn` in the page, with the same methods and the same `djinn:event` channel
  as the Electron preload, backed by the generated Connect clients.
- Wire in Go the calls used at start-up and to set up a project; the others answer "not yet",
  cleanly.
- Then migrate the interface screen by screen to the generated clients, and drop the shim.

## Done when
- [ ] The interface starts with no error, adds a project and finds it after a restart.
- [ ] No React file changed to get there.

## Open questions
- Which calls first? *Recommendation: the ones used at start-up and to set up a project (environment, load and save state, pick a folder, validate and index a project, open a link, notify); the others answer "not yet".*

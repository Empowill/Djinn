---
id: 01a1184f-cf1d-7255-b174-2c913792046b
code: T09
phase: 3
status: open
---

# T09 · Quality of life and clean-up

Delegable, not needed to start testing. Given to Djinn itself once phase 2 is done.

- [ ] The lead's terminal pinned at the bottom of the flight plan: moved to T21, in phase 2.
- [ ] An inbox for instructions added while an agent works, with acknowledgements.
- [ ] **The page keeps your place.** When something above what you are reading changes (a
  question answered and removed, a section added), the window stays on what you read: it never
  jumps up. Browsers do it by scroll anchoring; WebKit, the engine of the window on macOS and
  Linux, may lack it (to check), so a small script keeps the element in view as a fallback.
  An end-to-end test reads a question halfway down, removes one above, and checks that the
  question did not move on screen.
- [ ] MCP, as a thin layer over the command line.
- [ ] Native notifications and a global shortcut.
- [ ] OpenAPI documentation of the public methods.
- [ ] Analytics with DuckDB: who uses what, for how long.
- [ ] Remove Electron and the code it no longer needs.
- [ ] CPU limits per worker on Linux (systemd delegation).
- [ ] Shared team settings versioned in the repository.
- [ ] macOS specifics: no cgroups, pause by signal.
- [ ] Later, after v1: trusted machines and distributed work, see T15.

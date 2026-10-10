---
id: 01a126be-a4ef-7478-9fdd-141a21e56469
code: T31
phase: 3
status: draft
---

# T31 · Usage analytics with DuckDB

**Goal.** Track and analyze long-term resource and token usage across projects and wishes with DuckDB.

## Design
- Analytics with DuckDB: who uses what, for how long, and token costs per project and per wish.
- An embedded database querying exported events and usage metrics without overloading the primary SQLite store.
- Requires a decision on which metrics and dimensions to collect and aggregate before implementation.

## Open questions
- Which usage dimensions to record: tokens, costs, durations, tool call counts, or model families?
- Storage format: Parquet files written by Djinn and queried with DuckDB, or a persistent DuckDB database file?

## Done when
- [ ] Usage metrics and long-term analytics can be queried with DuckDB across wishes and projects. (needs: an agent, after a decision on what to collect)

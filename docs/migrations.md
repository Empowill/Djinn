# Migrations of the local settings

A Djinn installation keeps settings outside its database: `settings.json` and the developer's project files
(`projects/<id>/settings.txtpb`) in its data folder. When a build reads them in a new shape, a migration shipped in the
binary changes them once, at `djinn up` (`plan.Migrate`, `internal/plan/migrate.go`). The database has its own,
per part, at `store.Open`: see the documentation site, "A migration that fails".

## Rules (Q56)

- **Proto versioning first.** Field numbers, unknown fields kept or ignored, `reserved`: a new or retired field needs no
  migration. A migration is the last resort.
- **Only for a cost measured.** A migration fixes something a user sees or pays (a wrong value shown, a warning
  hidden), named in its comment with the decision or study that measured it. Never one for tidiness.
- **In the binary, at start.** Each migration runs at `djinn up`, before the store opens and before anything reads the
  settings it changes. Nothing else migrates on read.
- **Recorded.** A migration that applied adds its name to `migrations` in `settings.json` (`ui.v1.Settings`) and does
  not run again. A name never changes.
- **Always idempotent.** Run twice, a migration changes nothing the second time: it writes a file only when the file
  holds what it migrates. A test runs it twice and compares the bytes.
- **Never a failed start.** A migration that cannot apply (a file it cannot read, a write refused, a panic) returns an
  error: Djinn logs it as a warning, leaves that data untouched, does not record the migration, and tries again at the
  next start. A value it cannot carry over is dropped only when keeping it costs what the migration fixes, and is logged.
- **One mechanism.** Add a migration to the `migrations` list; never a second runner beside it.
- **Tested** on a blank data folder and on a folder in the state it changes (shapes copied from real ones, no real
  data).

## The migrations

| Name | Since | What it does |
| --- | --- | --- |
| `circle-from-project-settings` | W103 (L20, Q56 A) | Drops `provider`, `model`, `question_provider` and `question_model` from each developer project file. When the Summoning Circle is not configured yet (no lead seat, no row), it seeds the circle with them first; a value it cannot carry (a model this build refuses, a question model whose provider cannot run read-only) is dropped all the same. Once the circle is configured (a row set to **Same as lead** counts: it is a model choice, Q67), the lines are dropped without seeding: the circle already chose every worker, so no worker changes model. Once the circle is configured, every row nothing set is written Same as lead, so `settings.json` names every worker. They showed a wrong provider in the project's settings and hid the transcript warning. |

# Databases written by a release

`v0.4.0.db.gz` is the `djinn.db` the v0.4.0 release wrote (its Linux browser archive, SHA256 checked against the
release's `SHA256SUMS`), started on an empty data folder with no agent on the PATH: one project outside Git
(`fixture-project`), one wish, three questions (Q01 answered A with a note, Q02 open, Q03 the edit question of W1),
two tasks (W1 failed for want of claude, W2 pending after W1), and a report block (`Written by v0.4.0`) beside the
wish's creation block. The folder the data lived in was then replaced, byte for byte, by a neutral path of the same
length (`/home/user/djinn-fixture/xxx…`), which keeps every protobuf length intact, and the file vacuumed.

`TestEntities` (`plan_test.go`) opens a copy with this code and reads it all again. When a release
changes the store, add its database here the same way, and a case to the test.

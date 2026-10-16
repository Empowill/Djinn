# Gates

Workers share one machine. Their heavy commands, tests, builds and code generation, run through a gate of the
running Djinn: one holder at a time, granted only while the machine is not under pressure, the first wish first. Djinn
records what each command cost, and next time waits until the machine has the memory it peaked at.

A holder keeps its gate an hour at most. A lit [ifrit](#ifrit) keeps its gates as long as it is lit: they go back when
it goes out, at its out time at the latest, which each of your actions in Djinn pushes back, or when it ends. It goes
before no one: the first wish first, then the first come. Those who wait read "held by ifrit W12: task run-back;
released when you leave, at 15:42 at the latest", `djinn gate list` says `ifrit: true` with its `expire_time`, and the
Djinn's brief says who waits on it, so that the Djinn offers you to end it.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Machine and load](#machine-and-load), [Merges and corrections](#merges-and-corrections).

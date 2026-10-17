# Machine and load

The machine decides how many workers run: one per 2 cores and per 2 GiB of memory. The **operating load**, from
Minimal to Overclock, or Auto, sets how much of the machine Djinn takes. On Auto it yields the machine to the programs
outside Djinn, never to its own work, nor to you watching it: when what they use stays above 30 % of the CPU for 2
minutes, it goes one level down; when it stays below 15 % for 10 minutes, one level up towards the level you chose.
Hovering the slider says why, in one line. The **Hourglass** shows the machine, and each running worker, as time goes
by; its Djinn line counts every process Djinn started, those a worker left behind included.

**The disk.** The Go builds Djinn runs, in its workers and its checks, share one Go build cache across worktrees: an
unchanged package is compiled once, not once per worktree. Before a worker starts and before push checks, Djinn reads
the free space of its data folder's disk and of the Go build cache's, when Go is installed. Under 30 GB free, it
deletes the cache entries unused for a day. Under 10 GB, the Djinn tells you the disk is almost full and names the
biggest folders it knows, and the push checks wait until space comes back, instead of failing. Both thresholds are
flags of djinn up.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Gates](#gates), [Usage and cost](#usage-and-cost).

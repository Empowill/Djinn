# Machine and load

The machine decides how many workers run: one per 2 cores and per 2 GiB of memory. The **operating load**, from
Minimal to Overclock, or Auto, sets how much of the machine Djinn takes. On Auto it yields the machine to the programs
outside Djinn, never to its own work, nor to you watching it. It reads what they use as an average of the last 3
minutes: when that stays above 30 % of the CPU for 3 minutes, it goes one level down; when it stays below 15 % for 5
minutes, one level up towards the level you chose. A level is kept at least 3 minutes, and a load that hovers near a
mark changes nothing: the wait to go up starts again only above 20 %, the wait to go down only below 25 %. Hovering the
slider says why, in one line. The **Hourglass** shows the machine, and each running worker, as time goes by; its Djinn
line counts every process Djinn started, those a worker left behind included. On Linux it reads the systemd scopes of
Djinn, of its workers and of its own checks, which run in a scope of their own: a test that ends leaves the time it
used on the Djinn line, never on the programs outside.

**Pressure.** A worker starts, and a gate is granted, only while the machine is not under pressure: while tasks waited
for a core less than 40 % of the last 10 seconds on Minimal, 65 % on Medium, 80 % on High, 90 % on Max and 95 % on
Overclock; memory has limits of its own. On a machine of many cores, a test run's bursts reach 30 to 57 % while half
the machine is idle: from Medium up, they hold no worker.

**The disk.** The **Disk** tab of the Hourglass lists the main folders that use the disk, biggest first, with their
size, measured by the server. The Go builds Djinn runs, in its workers and its checks, share one Go build cache across
worktrees: an unchanged package is compiled once, not once per worktree. Before a worker starts and before push checks,
Djinn reads the free space of its data folder's disk and of the Go build cache's, when Go is installed. Under 30 GB
free, it deletes the cache entries unused for a day. Under 10 GB, the Djinn tells you the disk is almost full and names
the biggest folders it knows, and the push checks wait until space comes back, instead of failing. Both thresholds are
flags of djinn up.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Gates](#gates), [Usage and cost](#usage-and-cost).

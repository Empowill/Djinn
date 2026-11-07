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

**CPU priority.** The window, Djinn's server and the lead come first. The checks Djinn runs (before a merge, before a
push, the setup, a generation) come next, at nice 5: every merge waits for them. The workers, what they start, and the
local stack a worker runs (a serve) come last, at nice 10 on Linux and macOS, below normal on Windows, at every load
level but Overclock. Under a heavy load, typing in the window stays fluid: a keystroke no longer waits behind them. No
CPU is lost: when nothing else asks for it, they use all of it. At Overclock, the workers run at Djinn's own priority,
beside the window, and the checks with them, never below them; a serve stays low. A worker keeps the priority of the
level it started under. An install you start from the window runs at Djinn's own priority, as you wait for it; one
Djinn starts by itself runs as a check.

**Pressure.** A worker starts, and a gate is granted, only while the machine is not under pressure: while tasks waited
for a core less than 40 % of the last 10 seconds on Minimal, 65 % on Medium, 80 % on High, 90 % on Max and 95 % on
Overclock; memory has limits of its own. On a machine of many cores, a test run's bursts reach 30 to 57 % while half
the machine is idle: from Medium up, they hold no worker.

**The memory brake.** The gates share the machine; what escapes them, a process a command left running, an agent
without a hook, or the sum of the workers, can still fill its memory. Under 10 % of the memory available, or when every
task waited for memory a fifth of the last 10 seconds, two readings in a row, Djinn freezes the worker whose memory
grew most over the last minute while it held no gate nor admission, and its containers with it. Frozen 30 seconds with
the memory still short, it is interrupted, and resumes later in its session. A worker whose command a gate admitted is
never interrupted: only when admitted workers alone grow is the last one admitted frozen, and its command lives on.
After a minute of calm, the frozen workers thaw one at a time, the last frozen first. The Djinn hears each freeze,
interruption and thaw; a frozen worker's time is left out of its budget. The percent is a flag of djinn up; 0 turns the
brake off.

**The disk.** The **Disk** tab of the Hourglass draws a bar of the whole disk (with the 30 GB and 10 GB guard marks)
above two tabs: **Djinn and its workers** (worktrees, Playwright, tilasms, scratch, logs, and any isolated Go build
caches) and **Machine** (the Go build and module caches, Docker build cache, images and volumes when Docker is
installed, pnpm, uv, pip, npm, and JetBrains), grouped by risk (**Safe**, **Rebuilds**, **Data loss**) with each item's
size, what its cleanup frees, and the command to copy (plus one row at the bottom to copy all safe commands at once;
Djinn never runs a cleanup command itself). The Go builds Djinn runs, in its workers and its checks, share one Go build cache across
worktrees: an unchanged package is compiled once, not once per worktree. Before a worker starts and before push checks,
Djinn reads the free space of its data folder's disk and of the Go build cache's, when Go is installed. Under 30 GB
free, it deletes the cache entries unused for a day. Under 10 GB, the Djinn tells you the disk is almost full and names
the biggest folders it knows, and the push checks wait until space comes back, instead of failing. Both thresholds are
flags of djinn up.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Gates](#gates), [Usage and cost](#usage-and-cost).

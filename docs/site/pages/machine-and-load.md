# Machine and load

The machine decides how many workers run: one per 2 cores and per 2 GiB of memory. The **operating load**, from
Minimal to Overclock, or Auto, sets how much of the machine Djinn takes. On Auto it yields the machine to the programs
outside Djinn, never to its own work, nor to you watching it: when what they use stays above 30 % of the CPU for 2
minutes, it goes one level down; when it stays below 15 % for 10 minutes, one level up towards the level you chose.
Hovering the slider says why, in one line. The **Hourglass** shows the machine, and each running worker, as time goes
by; its Djinn line counts every process Djinn started, those a worker left behind included.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Gates](#gates), [Usage and cost](#usage-and-cost).

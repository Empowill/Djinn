# Worker memory: hard limits and dynamic guardrails

Djinn isolates each worker process tree in a systemd user scope on Linux (cgroup v2), allowing both CPU
and memory to be monitored and bounded.

Memory limits can be configured in two ways:
1. **`--worker-memory`**: A single, static hard ceiling (`MemoryMax`).
2. **`--worker-memory-guard`**: A dynamic, provider-adaptive memory guardrail.

Both options are disabled by default (`0`).

---

## Why a single static ceiling is risky

Setting a single, global memory ceiling for all workers via `--worker-memory` is an expert setting.
It is **never tied to operating load notches** (Decision Q68).

> **Too low, it kills heavy tasks; high enough, it protects against nothing.**

Worker tasks have vastly different memory requirements:
- A watcher or lightweight documentation task may need only 50 to 100 MiB.
- A standard code-editing agent session typically consumes 200 to 500 MiB.
- A heavy build, compiler pass, or end-to-end browser test may peak at 2 to 4 GiB or more.

If `--worker-memory` is set to 512 MiB to catch runaway processes, heavy tasks get killed by the OOM killer
before they can finish. Conversely, if it is set to 4 GiB so heavy tasks survive, an errant agent loop or
memory leak in a lightweight task can consume gigabytes of memory without hitting the ceiling.

For this reason, Djinn's operating load notches (quiet, relaxed, normal, loaded, saturated) scale concurrency
and load thresholds, but leave worker memory uncapped by default.

---

## Dynamic memory guardrails: `--worker-memory-guard`

The optional memory guardrail adapts each worker's `MemoryMax` to the observed memory profile of its provider
(Claude, Codex, Antigravity, etc.):

```sh
# Set dynamic guardrail at 2.5x observed provider peak (recommended: 2.0 to 3.0)
djinn up --worker-memory-guard 2.5

# Or via environment variable
export DJINN_WORKER_MEMORY_GUARD=2.5
djinn up
```

### How the guardrail is computed

1. **Observed peak memory:**
   For a given task provider, Djinn takes the median peak memory across the **latest 10 finished tasks** of that
   provider (the same statistical baseline computed for dispatch forecasting). Watchers and tasks without recorded
   resource usage are excluded.

2. **Multiplier and forecast floor:**
   When enabled with factor $F$ (e.g., $2.5$), the ceiling is scaled to $F \times \text{peak}$.
   To prevent premature termination on slight normal variations, the ceiling is also floored at the current
   memory forecast ($\text{peak} + \text{margin}$, where margin defaults to 256 MiB):
   $$\text{MemoryMax} = \max\bigl(F \times \text{peak},\; \text{peak} + \text{margin}\bigr)$$

3. **Without measurements, no ceiling:**
   If no finished tasks have been recorded for a provider yet ($\text{measured} = 0$), **no ceiling is enforced**
   (`MemoryMax = 0`). A worker is never capped blindly without empirical data.

4. **Interaction with `--worker-memory`:**
   If both a hard ceiling (`--worker-memory`) and a dynamic guardrail (`--worker-memory-guard`) are specified,
   Djinn applies the lower of the two:
   $$\text{Ceiling} = \min\bigl(\text{WorkerMemory},\; \text{GuardCeiling}\bigr)$$

---

## Comparison summary

| Feature | `--worker-memory` | `--worker-memory-guard` |
| :--- | :--- | :--- |
| **Type** | Static hard ceiling | Dynamic adaptive guardrail |
| **Default** | `0` (disabled) | `0` (disabled) |
| **Recommended range** | N/A (expert only) | `2.0` to `3.0` |
| **Adaptability** | None (same value for all tasks) | Per-provider, based on median of last 10 tasks |
| **Cold start behavior** | Applies immediately to all tasks | Uncapped until tasks complete and provide measurements |
| **Tied to load notches** | No (Decision Q68) | No (Decision Q68) |

---

## System requirements and delegation

Memory enforcement requires Linux cgroup v2 with memory delegation enabled for systemd user services.

If memory delegation is not enabled, systemd accepts `MemoryMax` properties but silently ignores them.
Djinn probes for this controller at startup. If the controller is missing, Djinn warns:

```text
djinn: memory limits per worker are off: systemd does not give the memory controller to your user
(an administrator adds Delegate=cpu cpuset io memory pids to user@.service, once)
```

To enable memory delegation, an administrator configures systemd:

```sh
# /etc/systemd/system/user@.service.d/delegate.conf
[Service]
Delegate=cpu cpuset io memory pids
```

Then reloads systemd: `systemctl daemon-reload`.

On macOS and Windows, systemd scopes are not supported; workers run in their respective process groups.

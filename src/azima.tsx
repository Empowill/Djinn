// An azima of the plan in the Tasks tab: a task no worker runs, with the work part of it under it. Its heading says
// where it stands (open, in progress, done: the lamp's, Task.azima), what it waits for (its after codes), and its
// progress. Opened, its parts, as the Tasks tab shows any task. An azima never waits for the person.
import { ChevronDown } from "lucide-react";
import { type ReactNode, useState } from "react";

import { AzimaState, type Task, TaskStatus } from "../gen/ts/plan/v1/plan_pb";
import { compareCodes, finishedTask } from "./data/flight";
import type { Tone } from "./data/format";
import { t, type TextKey } from "./i18n";
import { StatusBadge } from "./status";

// azimaTone and azimaLabel say where an azima stands: done in green, under way in blue while a worker runs on one of
// its parts, planned grey otherwise. Never orange: an azima waits for no one.
function azimaState(azima: Task): AzimaState {
  if (azima.status === TaskStatus.DONE) return AzimaState.DONE;
  return azima.azima?.state ?? AzimaState.OPEN;
}

function azimaTone(azima: Task): Tone {
  switch (azimaState(azima)) {
    case AzimaState.DONE:
      return "done";
    case AzimaState.IN_PROGRESS:
      return azima.azima?.partsRunning ? "running" : "planned";
  }
  return "planned";
}

const labels: Record<AzimaState, TextKey> = {
  [AzimaState.UNSPECIFIED]: "azima.state_open",
  [AzimaState.OPEN]: "azima.state_open",
  [AzimaState.IN_PROGRESS]: "azima.state_in_progress",
  [AzimaState.DONE]: "azima.state_done",
};

export function AzimaCard({
  azima,
  parts,
  tasks,
  origin,
  render,
}: {
  azima: Task;
  // The work part of it, in the order to show.
  parts: readonly Task[];
  // The tasks of its wish, by id: what it waits for.
  tasks: ReadonlyMap<string, Task>;
  // Where it comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  render: (task: Task) => ReactNode;
}) {
  const state = azimaState(azima);
  const [open, setOpen] = useState(
    state !== AzimaState.DONE && parts.some((x) => !finishedTask(x)),
  );
  const deps = azima.dependsOn
    .map((id) => tasks.get(id))
    .filter((x): x is Task => !!x);
  const after = deps.map((x) => x.code).sort(compareCodes);
  const waits = deps
    .filter((x) => x.status !== TaskStatus.DONE)
    .map((x) => x.code)
    .sort(compareCodes);
  const done = parts.filter((x) => x.status === TaskStatus.DONE).length;
  return (
    <section
      className={`azima-card tone-${azimaTone(azima)} ${open ? "open" : ""}`}
      id={`task-${azima.id}`}
      aria-label={`${azima.code} ${azima.title}`}
    >
      <button
        className="azima-heading"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <StatusBadge tone={azimaTone(azima)} label={t(labels[state])} />
        <span className="agent-code">{azima.code}</span>
        <strong title={azima.title}>{azima.title}</strong>
        <span className="azima-meta">
          {origin}
          {state !== AzimaState.DONE && waits.length > 0 && (
            <span
              className="azima-waits"
              title={t("page.after", { tasks: after.join(", ") })}
            >
              {t("azima.waits", { tasks: waits.join(", ") })}
            </span>
          )}
          {state !== AzimaState.DONE &&
            waits.length === 0 &&
            after.length > 0 && (
              <span className="azima-after">
                {t("page.after", { tasks: after.join(", ") })}
              </span>
            )}
          {parts.length > 0 && (
            <span
              className="azima-progress"
              title={t("azima.progress", { done, count: parts.length })}
            >
              <span className="azima-bar" aria-hidden="true">
                <span style={{ width: `${(100 * done) / parts.length}%` }} />
              </span>
              {done}/{parts.length}
            </span>
          )}
        </span>
        <ChevronDown size={14} className={open ? "rotated" : ""} />
      </button>
      {open && (
        <div className="azima-parts">
          {parts.length === 0 ? (
            <p className="muted-text">{t("azima.no_parts")}</p>
          ) : (
            parts.map(render)
          )}
        </div>
      )}
    </section>
  );
}

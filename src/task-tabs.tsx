// The tasks have a tab of their own, in a wish's view and in the flight plan, next to the rest. In it, a list: at the
// top what moves or waits for someone, by status (running, cut short and failed first, then waiting and paused, the
// planned ones last); then the azimas of the plan, each with the work part of it (src/azima.tsx); at the bottom every
// finished task, the latest ended first. The decisions have the next tab
// (src/decision-log.tsx), then a wish's tilasms (src/tilasms.tsx), and last, discreet, its blocks for agents
// (src/agent-blocks.tsx).
import { type ReactNode } from "react";

import { t } from "./i18n";

export type View = "main" | "tasks" | "decisions" | "tilasms" | "agents";

export function ViewTabs({
  view,
  main,
  tasks,
  decisions,
  tilasms,
  agents,
  onView,
}: {
  view: View;
  // The name of the rest: the wish, the flight plan.
  main: string;
  // How many tasks the Tasks tab holds.
  tasks: number;
  // How many decisions the Decisions tab holds.
  decisions: number;
  // How many tilasms the Tilasms tab holds; without it, a view with no such tab (the flight plan).
  tilasms?: number;
  // How many blocks the For agents tab holds; without it, a view with no such tab (the flight plan).
  agents?: number;
  onView: (view: View) => void;
}) {
  const tabs: { id: View; label: string; count: number }[] = [
    { id: "main", label: main, count: -1 },
    { id: "tasks", label: t("wish.tasks"), count: tasks },
    { id: "decisions", label: t("tabs.decisions"), count: decisions },
  ];
  if (tilasms !== undefined)
    tabs.push({ id: "tilasms", label: t("tabs.tilasms"), count: tilasms });
  if (agents !== undefined)
    tabs.push({ id: "agents", label: t("tabs.agents"), count: agents });
  return (
    <div className="view-tabs" role="tablist">
      {tabs.map(({ id, label, count }) => (
        <button
          key={id}
          type="button"
          role="tab"
          id={`view-tab-${id}`}
          aria-selected={view === id}
          className={`${view === id ? "active" : ""}${id === "agents" ? " discreet" : ""}`.trim()}
          onClick={() => onView(id)}
        >
          {label}
          {count >= 0 && <span className="count">{count}</span>}
        </button>
      ))}
    </div>
  );
}

// TaskSections lays out the Tasks tab: the tasks that move or wait, then the azimas with the work part of them, then
// the finished ones, each in the order given. Without azimas, their section is hidden.
export function TaskSections<T, A = never>({
  moving,
  finished,
  render,
  azimas = [],
  renderAzima,
  aside,
}: {
  moving: readonly T[];
  finished: readonly T[];
  render: (item: T) => ReactNode;
  azimas?: readonly A[];
  renderAzima?: (azima: A) => ReactNode;
  // Beside the first title: what the tasks spent.
  aside?: ReactNode;
}) {
  return (
    <div role="tabpanel" aria-labelledby="view-tab-tasks">
      <section
        className="wish-section tasks-moving"
        aria-label={t("tasks.live")}
      >
        <div className="section-title">
          <h2>
            {t("tasks.live")}
            <span className="count">{moving.length}</span>
          </h2>
          {aside}
        </div>
        {moving.length === 0 && (
          <p className="muted-text">{t("tasks.none_live")}</p>
        )}
        {moving.map(render)}
      </section>
      {azimas.length > 0 && renderAzima && (
        <section
          className="wish-section tasks-azimas"
          aria-label={t("tasks.azimas")}
        >
          <div className="section-title">
            <h2>
              {t("tasks.azimas")}
              <span className="count">{azimas.length}</span>
            </h2>
          </div>
          {azimas.map(renderAzima)}
        </section>
      )}
      <section
        className="wish-section tasks-finished"
        aria-label={t("tasks.done")}
      >
        <div className="section-title">
          <h2>
            {t("tasks.done")}
            <span className="count">{finished.length}</span>
          </h2>
        </div>
        {finished.length === 0 && (
          <p className="muted-text">{t("tasks.none_done")}</p>
        )}
        {finished.map(render)}
      </section>
    </div>
  );
}

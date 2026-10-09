// The tasks have a tab of their own, in a wish's view and in the flight plan, next to the rest. In it, a list: at the
// top what moves or waits for someone, by status (running, cut short and failed first, then waiting and paused, the
// planned ones last); at the bottom every finished task, the latest ended first. The decisions have the next tab
// (src/decision-log.tsx).
import { type ReactNode } from "react";

import { t } from "./i18n";

export type View = "main" | "tasks" | "decisions";

export function ViewTabs({
  view,
  main,
  tasks,
  decisions,
  onView,
}: {
  view: View;
  // The name of the rest: the wish, the flight plan.
  main: string;
  // How many tasks the Tasks tab holds.
  tasks: number;
  // How many decisions the Decisions tab holds.
  decisions: number;
  onView: (view: View) => void;
}) {
  const tabs = [
    { id: "main", label: main, count: -1 },
    { id: "tasks", label: t("wish.tasks"), count: tasks },
    { id: "decisions", label: t("tabs.decisions"), count: decisions },
  ] as const;
  return (
    <div className="view-tabs" role="tablist">
      {tabs.map(({ id, label, count }) => (
        <button
          key={id}
          type="button"
          role="tab"
          id={`view-tab-${id}`}
          aria-selected={view === id}
          className={view === id ? "active" : ""}
          onClick={() => onView(id)}
        >
          {label}
          {count >= 0 && <span className="count">{count}</span>}
        </button>
      ))}
    </div>
  );
}

// TaskSections lays out the Tasks tab: the tasks that move or wait, then the finished ones, each in the order given.
export function TaskSections<T>({
  moving,
  finished,
  render,
  aside,
}: {
  moving: readonly T[];
  finished: readonly T[];
  render: (item: T) => ReactNode;
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

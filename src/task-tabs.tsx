// The tasks have a tab of their own, in a wish's view and in the flight plan, next to the rest. In it, a list: at the
// top the azimas of the plan, each with the work part of it (src/azima.tsx), the done ones folded (Fold); then what
// moves or waits for someone, by status (running, cut short and failed first, then waiting and paused, the planned
// ones last); at the bottom every finished task, the latest ended first. The decisions have the next tab
// (src/decision-log.tsx), then a wish's tilasms (src/tilasms.tsx), and last, discreet, its blocks for agents
// (src/agent-blocks.tsx).
import { ChevronDown } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

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

// What a click unfolded, by the key of its Fold: it stays so while the window lives, a tab or a wish left and back.
const unfolded = new Set<string>();

// Fold hides what is finished behind one line, "show the N finished": none of it is rendered until a click unfolds
// it, and the line, then "hide the finished ones", folds it back. With open, it unfolds by itself: a link brings one
// of them into sight.
export function Fold({
  id,
  count,
  open = false,
  children,
}: {
  // Its key for the window's session: the same key, the same state.
  id: string;
  count: number;
  open?: boolean;
  children: ReactNode;
}) {
  const [shown, setShown] = useState(() => open || unfolded.has(id));
  const show = (next: boolean) => {
    if (next) unfolded.add(id);
    else unfolded.delete(id);
    setShown(next);
  };
  useEffect(() => {
    if (!open) return;
    unfolded.add(id);
    setShown(true);
  }, [open, id]);
  if (count === 0) return null;
  return (
    <>
      <button
        type="button"
        className="fold-line"
        aria-expanded={shown}
        onClick={() => show(!shown)}
      >
        <ChevronDown size={14} className={shown ? "rotated" : ""} />
        {shown ? t("tasks.hide_finished") : t("tasks.show_finished", { count })}
      </button>
      {shown && children}
    </>
  );
}

// TaskSections lays out the Tasks tab: azimas first, then free tasks that move or wait, then finished tasks. Done
// azimas stay behind their one global Fold. Without azimas, free tasks keep their usual live heading.
export function TaskSections<T, A = never>({
  moving,
  finished,
  render,
  azimas = [],
  doneAzimas = [],
  renderAzima,
  fold = "",
  showDone = false,
  workspaceHierarchy = false,
  aside,
}: {
  moving: readonly T[];
  finished: readonly T[];
  render: (item: T) => ReactNode;
  // The azimas not done yet, then the done ones, behind their Fold.
  azimas?: readonly A[];
  doneAzimas?: readonly A[];
  renderAzima?: (azima: A) => ReactNode;
  // The key of the done azimas' Fold: the wish's id, or the flight plan's.
  fold?: string;
  // The done azimas show: a link brings one of them, or one of their parts, into sight.
  showDone?: boolean;
  // The wish workspace groups Azimas and free tasks, each with moving and finished subgroups.
  workspaceHierarchy?: boolean;
  // Beside the first title: what the tasks spent.
  aside?: ReactNode;
}) {
  if (workspaceHierarchy) {
    const hasAzimas = azimas.length + doneAzimas.length > 0;
    const hasFreeTasks = moving.length + finished.length > 0;

    return (
      <div
        className="workspace-task-hierarchy"
        role="tabpanel"
        aria-labelledby="view-tab-tasks"
      >
        {hasAzimas && (
          <section
            className="wish-section workspace-task-group tasks-azimas"
            aria-label={t("tasks.azimas")}
          >
            <div className="section-title">
              <h2>{t("tasks.azimas")}</h2>
            </div>
            <div className="workspace-task-subgroups">
              {azimas.length > 0 && (
                <section
                  className="workspace-task-subgroup tasks-azimas-moving"
                  aria-label={t("tasks.in_progress")}
                >
                  <div className="workspace-task-subgroup-heading">
                    <h3>
                      {t("tasks.in_progress")}
                      <span className="count">{azimas.length}</span>
                    </h3>
                  </div>
                  {renderAzima && (
                    <div className="card-grid azima-grid">
                      {azimas.map(renderAzima)}
                    </div>
                  )}
                </section>
              )}
              {doneAzimas.length > 0 && (
                <section
                  className="workspace-task-subgroup tasks-azimas-finished"
                  aria-label={t("tasks.done")}
                >
                  <div className="workspace-task-subgroup-heading">
                    <h3>
                      {t("tasks.done")}
                      <span className="count">{doneAzimas.length}</span>
                    </h3>
                  </div>
                  {renderAzima && (
                    <div className="card-grid azima-grid">
                      {doneAzimas.map(renderAzima)}
                    </div>
                  )}
                </section>
              )}
            </div>
          </section>
        )}
        {hasFreeTasks && (
          <section
            className="wish-section workspace-task-group tasks-free"
            aria-label={t("wish.tasks")}
          >
            <div className="section-title">
              <h2>{t("wish.tasks")}</h2>
              {aside}
            </div>
            <div className="workspace-task-subgroups">
              {moving.length > 0 && (
                <section
                  className="workspace-task-subgroup tasks-free-moving"
                  aria-label={t("tasks.in_progress")}
                >
                  <div className="workspace-task-subgroup-heading">
                    <h3>
                      {t("tasks.in_progress")}
                      <span className="count">{moving.length}</span>
                    </h3>
                  </div>
                  <div className="card-grid task-grid">
                    {moving.map(render)}
                  </div>
                </section>
              )}
              {finished.length > 0 && (
                <section
                  className="workspace-task-subgroup tasks-free-finished"
                  aria-label={t("tasks.done")}
                >
                  <div className="workspace-task-subgroup-heading">
                    <h3>
                      {t("tasks.done")}
                      <span className="count">{finished.length}</span>
                    </h3>
                  </div>
                  <div className="card-grid task-grid">
                    {finished.map(render)}
                  </div>
                </section>
              )}
            </div>
          </section>
        )}
      </div>
    );
  }
  const hasAzimas = azimas.length + doneAzimas.length > 0;
  const freeTasksLabel = hasAzimas ? t("tasks.other") : t("tasks.live");
  const showMoving = moving.length > 0 || !hasAzimas;
  return (
    <div role="tabpanel" aria-labelledby="view-tab-tasks">
      {hasAzimas && renderAzima && (
        <section
          className="wish-section tasks-azimas"
          aria-label={t("tasks.azimas")}
        >
          <div className="section-title">
            <h2>
              {t("tasks.azimas")}
              <span className="count">{azimas.length + doneAzimas.length}</span>
            </h2>
            {aside}
          </div>
          {azimas.length > 0 && (
            <div className="card-grid azima-grid">
              {azimas.map(renderAzima)}
            </div>
          )}
          <Fold id={`azimas:${fold}`} count={doneAzimas.length} open={showDone}>
            <div className="card-grid azima-grid">
              {doneAzimas.map(renderAzima)}
            </div>
          </Fold>
        </section>
      )}
      {showMoving && (
        <section
          className="wish-section tasks-moving"
          aria-label={freeTasksLabel}
        >
          <div className="section-title">
            <h2>
              {freeTasksLabel}
              <span className="count">{moving.length}</span>
            </h2>
            {!hasAzimas && aside}
          </div>
          {moving.length === 0 && (
            <p className="muted-text">{t("tasks.none_live")}</p>
          )}
          {moving.length > 0 && (
            <div className="card-grid task-grid">{moving.map(render)}</div>
          )}
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
        {finished.length > 0 && (
          <div className="card-grid task-grid">{finished.map(render)}</div>
        )}
      </section>
    </div>
  );
}

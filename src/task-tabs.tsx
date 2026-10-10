// The tasks have a tab of their own, in a wish's view and in the flight plan, next to the rest. In it, a list: at the
// top what moves or waits for someone, by status (running, cut short and failed first, then waiting and paused, the
// planned ones last); then the azimas of the plan, each with the work part of it (src/azima.tsx), the done ones folded
// (Fold). The decisions have the next tab (src/decision-log.tsx), then a wish's tilasms (src/tilasms.tsx), and last,
// discreet, its blocks for agents (src/agent-blocks.tsx).
import { ChevronDown } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import { Change } from "../gen/ts/plan/v1/plan_pb";
import { t } from "./i18n";

export type View = "main" | "tasks" | "decisions" | "tilasms" | "agents";

const KINDS_MAIN: readonly Change[] = [
  Change.TASK,
  Change.QUESTION,
  Change.BLOCK,
];
const KINDS_TASKS: readonly Change[] = [Change.TASK];
const KINDS_DECISIONS: readonly Change[] = [
  Change.QUESTION,
  Change.BLOCK,
  Change.TASK,
];
const KINDS_TILASMS: readonly Change[] = [Change.TILASM, Change.TASK];
const KINDS_AGENTS: readonly Change[] = [Change.BLOCK, Change.TASK];

// kindsForView gives the kinds of changes a view needs to display: a tab loaded on demand reads only its kinds.
export function kindsForView(view: View): readonly Change[] {
  switch (view) {
    case "main":
      return KINDS_MAIN;
    case "tasks":
      return KINDS_TASKS;
    case "decisions":
      return KINDS_DECISIONS;
    case "tilasms":
      return KINDS_TILASMS;
    case "agents":
      return KINDS_AGENTS;
  }
}

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

// TaskSections lays out the Tasks tab: the tasks that move or wait, then the azimas with the work part of them, the
// done ones folded. Without azimas, their section is hidden.
export function TaskSections<T, A = never>({
  moving,
  render,
  azimas = [],
  doneAzimas = [],
  renderAzima,
  fold = "",
  showDone = false,
  aside,
}: {
  moving: readonly T[];
  render: (item: T) => ReactNode;
  // The azimas not done yet, then the done ones, behind their Fold.
  azimas?: readonly A[];
  doneAzimas?: readonly A[];
  renderAzima?: (azima: A) => ReactNode;
  // The key of the done azimas' Fold: the wish's id, or the flight plan's.
  fold?: string;
  // The done azimas show: a link brings one of them, or one of their parts, into sight.
  showDone?: boolean;
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
        {moving.length > 0 && (
          <div className="card-grid task-grid">{moving.map(render)}</div>
        )}
      </section>
      {azimas.length + doneAzimas.length > 0 && renderAzima && (
        <section
          className="wish-section tasks-azimas"
          aria-label={t("tasks.azimas")}
        >
          <div className="section-title">
            <h2>
              {t("tasks.azimas")}
              <span className="count">{azimas.length + doneAzimas.length}</span>
            </h2>
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
    </div>
  );
}

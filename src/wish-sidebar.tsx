// The side panel's lists, read from the services: the flight plan's entry with what waits for you there (questions,
// new inbox items), the wishes, the active ones by rank (drag one up or down, or Alt+Arrow) and how many of the three
// places they take, then the paused ones and the granted ones; and the projects. The wishes' list shows three rows and
// scrolls under the pointer. A paused wish dragged onto an active one, or sent first with its button, becomes active
// there: the wish pushed past the third place is paused.
import {
  ArrowUpToLine,
  ChevronDown,
  ChevronRight,
  FolderOpen,
  GripVertical,
  Inbox,
  Plane,
  Plus,
  SquareTerminal,
} from "lucide-react";
import { type KeyboardEvent, useState } from "react";

import type { Project, Wish } from "../gen/ts/plan/v1/plan_pb";
import { WishState } from "../gen/ts/plan/v1/plan_pb";
import { MAX_ACTIVE, isActive, wishTone } from "./data/format";
import { t } from "./i18n";
import { useOpenTerminal } from "./lead-terminal";
import { CountPill, ToneIcon } from "./status";

// What the side panel counts in a wish.
export interface WishCounts {
  questions: number;
  running: number;
}

export function WishSidebar({
  wishes,
  projects,
  counts = {},
  inbox = 0,
  planSelected = false,
  onSelectPlan,
  selectedWishId,
  selectedProjectId,
  collapsed,
  onSelectWish,
  onSelectProject,
  onMove,
  onNewProject,
}: {
  wishes: Wish[];
  projects: Project[];
  // What each active wish holds, by id: questions that wait for you, workers that run.
  counts?: Readonly<Record<string, WishCounts>>;
  // How many inbox items wait for your answer: the flight plan shows them.
  inbox?: number;
  // The flight plan of the active wishes shows.
  planSelected?: boolean;
  onSelectPlan?: () => void;
  selectedWishId: string;
  selectedProjectId: string;
  collapsed: boolean;
  onSelectWish: (id: string) => void;
  onSelectProject: (id: string) => void;
  // Moves a wish to a rank, from 1: a paused one becomes active there.
  onMove: (wishId: string, to: number) => void;
  onNewProject: () => void;
}) {
  const active = wishes.filter(isActive);
  const paused = wishes.filter((w) => w.state === WishState.PAUSED);
  const granted = wishes.filter((w) => w.state === WishState.GRANTED);
  const [showGranted, setShowGranted] = useState(false);
  // Where djinn serves the page, a project opens a terminal at its root, in a tab of its own.
  const openTerminal = useOpenTerminal();
  const [dragged, setDragged] = useState("");

  const item = (wish: Wish, rank?: number) => {
    const count = counts[wish.id] ?? { questions: 0, running: 0 };
    const tone = wishTone(wish, count.questions, count.running);
    const keys = (event: KeyboardEvent) => {
      if (!rank || !event.altKey) return;
      if (event.key === "ArrowUp" && rank > 1) onMove(wish.id, rank - 1);
      else if (event.key === "ArrowDown" && rank < active.length)
        onMove(wish.id, rank + 1);
      else return;
      event.preventDefault();
    };
    const row = (
      <button
        key={wish.id}
        className={`mission-nav wish-nav ${wish.id === selectedWishId ? "selected" : ""} ${dragged === wish.id ? "dragged" : ""}`}
        onClick={() => onSelectWish(wish.id)}
        onKeyDown={keys}
        title={
          rank
            ? t("sidebar.wish_rank", { rank, title: wish.title })
            : wish.title
        }
        draggable={!!rank || wish.state === WishState.PAUSED}
        onDragStart={(event) => {
          event.dataTransfer.setData("text/plain", wish.id);
          event.dataTransfer.effectAllowed = "move";
          setDragged(wish.id);
        }}
        onDragEnd={() => setDragged("")}
        onDragOver={(event) => {
          if (rank && dragged) event.preventDefault();
        }}
        onDrop={(event) => {
          event.preventDefault();
          const id = event.dataTransfer.getData("text/plain");
          setDragged("");
          if (rank && id && id !== wish.id) onMove(id, rank);
        }}
      >
        <span className={`wish-tone tone-${tone}`}>
          <ToneIcon tone={tone} size={12} />
        </span>
        {!collapsed && (
          <>
            <span>{wish.title}</span>
            {count.questions > 0 && (
              <CountPill
                tone="waiting"
                count={count.questions}
                label={t("wish.questions_wait", { count: count.questions })}
              />
            )}
            {count.running > 0 && (
              <CountPill
                tone="running"
                count={count.running}
                label={t("plan.running", { count: count.running })}
              />
            )}
            {rank ? (
              <small className="wish-rank">
                <GripVertical size={11} aria-hidden="true" />
                {rank}
              </small>
            ) : null}
          </>
        )}
      </button>
    );
    if (wish.state !== WishState.PAUSED || collapsed) return row;
    return (
      <div key={wish.id} className="wish-row">
        {row}
        <button
          className="icon-button wish-first"
          title={t("sidebar.to_first")}
          aria-label={t("sidebar.to_first")}
          onClick={() => onMove(wish.id, 1)}
        >
          <ArrowUpToLine size={13} />
        </button>
      </div>
    );
  };

  const waits = active.reduce(
    (sum, w) => sum + (counts[w.id]?.questions ?? 0),
    0,
  );
  return (
    <>
      {onSelectPlan && (active.length > 0 || inbox > 0) && (
        <button
          className={`nav-item plan-nav ${planSelected ? "selected" : ""}`}
          onClick={onSelectPlan}
          title={t("plan.title")}
        >
          <Plane size={15} />
          {!collapsed && <span>{t("plan.title")}</span>}
          {waits > 0 && (
            <CountPill
              tone="waiting"
              count={waits}
              label={t("wish.questions_wait", { count: waits })}
            />
          )}
          {inbox > 0 && (
            <CountPill
              tone="waiting"
              icon={Inbox}
              count={inbox}
              label={t("sidebar.inbox_new", { count: inbox })}
            />
          )}
        </button>
      )}
      <div className="sidebar-section-label">
        {!collapsed && <span>{t("sidebar.wishes")}</span>}
        {!collapsed && (
          <span
            className={`nav-counter ${active.length >= MAX_ACTIVE ? "full" : ""}`}
            title={t("sidebar.active_detail", { max: MAX_ACTIVE })}
          >
            {t("sidebar.active_count", {
              count: active.length,
              max: MAX_ACTIVE,
            })}
          </span>
        )}
      </div>
      <div className="mission-list wish-list">
        {active.map((wish, i) => item(wish, wish.rank || i + 1))}
        {!active.length && !collapsed && (
          <p className="project-empty">{t("sidebar.no_active")}</p>
        )}
        {paused.length > 0 && (
          <>
            {!collapsed && (
              <div className="sidebar-section-label sub">
                <span>{t("sidebar.paused")}</span>
              </div>
            )}
            {paused.map((wish) => item(wish))}
          </>
        )}
        {granted.length > 0 && (
          <>
            {!collapsed && (
              <button
                className="sidebar-section-label sub"
                onClick={() => setShowGranted(!showGranted)}
                aria-expanded={showGranted}
              >
                {showGranted ? (
                  <ChevronDown size={12} />
                ) : (
                  <ChevronRight size={12} />
                )}
                <span>{t("sidebar.granted", { count: granted.length })}</span>
              </button>
            )}
            {showGranted && granted.map((wish) => item(wish))}
          </>
        )}
      </div>
      <div className="sidebar-section-label project-section-label">
        {!collapsed && <span>{t("sidebar.projects")}</span>}
        <button
          onClick={onNewProject}
          title={t("sidebar.new_project")}
          aria-label={t("sidebar.new_project")}
        >
          <Plus size={15} />
        </button>
      </div>
      <div className="mission-list project-list">
        {projects.map((project) => (
          <div
            key={project.id}
            className={`project-nav ${selectedProjectId === project.id ? "selected" : ""}`}
          >
            <button
              className="project-select"
              title={project.directory || t("project.no_folder")}
              onClick={() => onSelectProject(project.id)}
            >
              <FolderOpen size={collapsed ? 16 : 15} />
              {!collapsed && <span>{project.name}</span>}
            </button>
            {openTerminal && project.directory && !collapsed && (
              <button
                className="icon-button project-terminal"
                onClick={() => openTerminal(project)}
                title={t("sidebar.project_terminal", { project: project.name })}
                aria-label={t("sidebar.project_terminal", {
                  project: project.name,
                })}
              >
                <SquareTerminal size={13} />
              </button>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

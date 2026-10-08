// The side panel's lists, read from the services: the wishes, the active ones by rank (drag one up or down, or
// Alt+Arrow) and how many of the three places they take, then the paused ones and the granted ones; and the
// projects.
import {
  ChevronDown,
  ChevronRight,
  FolderOpen,
  GripVertical,
  Plane,
  Plus,
} from "lucide-react";
import { type KeyboardEvent, useState } from "react";

import type { Project, Wish } from "../gen/ts/plan/v1/plan_pb";
import { WishState } from "../gen/ts/plan/v1/plan_pb";
import { MAX_ACTIVE, isActive } from "./data/format";
import { t } from "./i18n";

export function WishSidebar({
  wishes,
  projects,
  waiting = {},
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
  // How many questions wait in each active wish, by id.
  waiting?: Readonly<Record<string, number>>;
  // The flight plan of the active wishes shows.
  planSelected?: boolean;
  onSelectPlan?: () => void;
  selectedWishId: string;
  selectedProjectId: string;
  collapsed: boolean;
  onSelectWish: (id: string) => void;
  onSelectProject: (id: string) => void;
  // Moves an active wish to a rank, from 1.
  onMove: (wishId: string, to: number) => void;
  onNewProject: () => void;
}) {
  const active = wishes.filter(isActive);
  const paused = wishes.filter((w) => w.state === WishState.PAUSED);
  const granted = wishes.filter((w) => w.state === WishState.GRANTED);
  const [showGranted, setShowGranted] = useState(false);
  const [dragged, setDragged] = useState("");

  const item = (wish: Wish, rank?: number) => {
    const keys = (event: KeyboardEvent) => {
      if (!rank || !event.altKey) return;
      if (event.key === "ArrowUp" && rank > 1) onMove(wish.id, rank - 1);
      else if (event.key === "ArrowDown" && rank < active.length)
        onMove(wish.id, rank + 1);
      else return;
      event.preventDefault();
    };
    return (
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
        draggable={!!rank}
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
        <span
          className={`mission-dot ${wish.ready ? "waiting" : rank ? "running" : wish.state === WishState.GRANTED ? "done" : "idle"}`}
        />
        {!collapsed && (
          <>
            <span>{wish.title}</span>
            {(waiting[wish.id] ?? 0) > 0 && (
              <small
                className="wish-waiting"
                title={t("wish.questions_wait", { count: waiting[wish.id] })}
              >
                {waiting[wish.id]}
              </small>
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
  };

  const waits = active.reduce((sum, w) => sum + (waiting[w.id] ?? 0), 0);
  return (
    <>
      {onSelectPlan && active.length > 0 && (
        <button
          className={`nav-item plan-nav ${planSelected ? "selected" : ""}`}
          onClick={onSelectPlan}
          title={t("plan.title")}
        >
          <Plane size={15} />
          {!collapsed && <span>{t("plan.title")}</span>}
          {waits > 0 && (
            <small
              className="wish-waiting"
              title={t("wish.questions_wait", { count: waits })}
            >
              {waits}
            </small>
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
          </div>
        ))}
      </div>
    </>
  );
}

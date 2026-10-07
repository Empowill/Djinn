import { useState } from "react";
import {
  Plus,
  FolderOpen,
  Settings2,
  ChevronRight,
  ChevronDown,
} from "lucide-react";
import type { Project, Task } from "./types";

function newestFirst<T>(items: T[], getDate: (item: T) => string) {
  return items
    .map((item, index) => ({
      item,
      index,
      timestamp: Date.parse(getDate(item)),
    }))
    .sort((a, b) => {
      const aTimestamp = Number.isFinite(a.timestamp)
        ? a.timestamp
        : Number.NEGATIVE_INFINITY;
      const bTimestamp = Number.isFinite(b.timestamp)
        ? b.timestamp
        : Number.NEGATIVE_INFINITY;
      return bTimestamp - aTimestamp || a.index - b.index;
    })
    .map(({ item }) => item);
}

export function ProjectSidebar({
  projects,
  tasks,
  selectedTaskId,
  selectedProjectId,
  collapsed,
  onSelectTask,
  onSelectProject,
  onNewProject,
  onEditProject,
  onNewMission,
}: {
  projects: Project[];
  tasks: Task[];
  selectedTaskId: string;
  selectedProjectId: string;
  collapsed: boolean;
  onSelectTask: (id: string) => void;
  onSelectProject: (id: string) => void;
  onNewProject: () => void;
  onEditProject: (id: string) => void;
  onNewMission: (id: string) => void;
}) {
  const [closed, setClosed] = useState<string[]>([]);
  const owner = (task: Task) =>
    projects.find((p) =>
      task.projectId ? p.id === task.projectId : p.directory === task.project,
    );
  const mission = (task: Task) => (
    <button
      key={task.id}
      className={`mission-nav ${task.id === selectedTaskId ? "selected" : ""}`}
      onClick={() => onSelectTask(task.id)}
      title={task.title}
    >
      <span className={`mission-dot ${task.status}`} />
      {!collapsed && (
        <>
          <span>{task.title.replace(/\n/g, " ")}</span>
          {task.demo && <small>DÉMO</small>}
        </>
      )}
    </button>
  );
  const orderedProjects = newestFirst(projects, (project) => project.updatedAt);
  return (
    <>
      <div className="sidebar-section-label project-section-label">
        {!collapsed && <span>PROJETS</span>}
        <button
          onClick={onNewProject}
          title="Créer un projet"
          aria-label="Créer un projet"
        >
          <Plus size={15} />
        </button>
      </div>
      <div className="mission-list project-list">
        {orderedProjects.map((project) => {
          const items = tasks.filter((t) => owner(t)?.id === project.id);
          const orderedItems = newestFirst(items, (task) => task.createdAt);
          const expanded = !closed.includes(project.id);
          return (
            <section key={project.id} className="sidebar-project">
              <div
                className={`project-nav ${selectedProjectId === project.id ? "selected" : ""}`}
              >
                <button
                  className="project-select"
                  title={project.name}
                  onClick={() => {
                    onSelectProject(project.id);
                    setClosed(
                      expanded && selectedProjectId === project.id
                        ? [...closed, project.id]
                        : closed.filter((id) => id !== project.id),
                    );
                  }}
                >
                  {collapsed ? (
                    <FolderOpen size={16} />
                  ) : (
                    <>
                      {expanded ? (
                        <ChevronDown size={13} />
                      ) : (
                        <ChevronRight size={13} />
                      )}
                      <FolderOpen size={15} />
                      <span>{project.name}</span>
                    </>
                  )}
                </button>
                {!collapsed && (
                  <>
                    <button
                      title={`Nouvelle mission dans ${project.name}`}
                      aria-label={`Nouvelle mission dans ${project.name}`}
                      onClick={() => onNewMission(project.id)}
                    >
                      <Plus size={14} />
                    </button>
                    <button
                      title={`Réglages de ${project.name}`}
                      aria-label={`Réglages de ${project.name}`}
                      onClick={() => onEditProject(project.id)}
                    >
                      <Settings2 size={14} />
                    </button>
                  </>
                )}
              </div>
              {(expanded || collapsed) && (
                <div className="project-missions">
                  {orderedItems.map(mission)}
                  {!items.length && !collapsed && (
                    <button
                      className="project-empty"
                      onClick={() => onNewMission(project.id)}
                    >
                      Créer une première mission
                    </button>
                  )}
                </div>
              )}
            </section>
          );
        })}
        {tasks.some((t) => !owner(t)) && (
          <section className="sidebar-project">
            {!collapsed && (
              <div className="sidebar-section-label">AUTRES MISSIONS</div>
            )}
            {newestFirst(
              tasks.filter((t) => !owner(t)),
              (task) => task.createdAt,
            ).map(mission)}
          </section>
        )}
      </div>
    </>
  );
}

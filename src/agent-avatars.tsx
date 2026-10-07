import { agentDisplayState } from "./agent-state";
import { useEffect, useId, useRef, type CSSProperties } from "react";
import { motion } from "motion/react";
import type { Agent, Task } from "./types";
import { agentColor } from "./visuals";
import "./agent-avatars.css";

const statusLabels: Record<Agent["status"], string> = {
  queued: "en attente",
  running: "en cours",
  blocked: "décision requise",
  done: "terminé",
  error: "en erreur",
};

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  return parts
    .slice(0, 2)
    .map((part) => part[0])
    .join("")
    .toUpperCase();
}

function AgentAvatar({
  agent,
  task,
  index,
  onClick,
  interactive = true,
}: {
  agent: Agent;
  task?: Task;
  index: number;
  onClick: () => void;
  interactive?: boolean;
}) {
  const display = task ? agentDisplayState(task, agent) : { status: agent.status, label: statusLabels[agent.status] };
  const label = `${agent.name} · ${display.label}`;
  const color = agentColor(agent.id, index);
  const content = (
    <>
      <span className="agent-avatar-face" aria-hidden="true">
        {initials(agent.name)}
      </span>
      <span className="agent-avatar-status" aria-hidden="true" />
    </>
  );
  const props = {
    className: `agent-avatar-button is-${display.status}`,
    style: { "--agent-avatar-color": color } as CSSProperties,
    title: agent.name,
    "data-tooltip": label,
  };
  return interactive ? (
    <button
      type="button"
      {...props}
      onClick={onClick}
      aria-label={`Ouvrir la conversation avec ${label}`}
    >
      {content}
    </button>
  ) : (
    <span {...props} aria-hidden="true">
      {content}
    </span>
  );
}

export function AgentAvatars({
  agents,
  task,
  onAgent,
  onOverflow,
}: {
  agents: Agent[];
  task?: Task;
  onAgent: (agent: Agent) => void;
  onOverflow: () => void;
}) {
  const visible = agents.slice(0, 3);
  const overflow = Math.max(0, agents.length - visible.length);
  if (!agents.length) return null;
  return (
    <div className="agent-avatars" aria-label="Agents de la mission">
      <span className="agent-avatars-label">Équipe</span>
      <div className="agent-avatar-list">
        {visible.map((agent, index) => (
          <AgentAvatar
            key={agent.id}
            agent={agent}
            task={task}
            index={agents.indexOf(agent)}
            onClick={() => onAgent(agent)}
          />
        ))}
        {overflow > 0 && (
          <button
            type="button"
            className="agent-avatar-overflow"
            onClick={onOverflow}
            aria-label={`Afficher les ${overflow} autres agents`}
            title={`Afficher les ${overflow} autres agents`}
          >
            +{overflow}
          </button>
        )}
      </div>
    </div>
  );
}

export function AgentPickerDrawer({
  agents,
  task,
  onClose,
  onAgent,
}: {
  agents: Agent[];
  task?: Task;
  onClose: () => void;
  onAgent: (agent: Agent) => void;
}) {
  const headingId = useId();
  const panel = useRef<HTMLElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const focusFirst = () => {
      panel.current
        ?.querySelector<HTMLElement>(
          'button:not([disabled]), [href], input, textarea, select',
        )
        ?.focus({ preventScroll: true });
    };
    const frame = requestAnimationFrame(focusFirst);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRef.current();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(
        panel.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input, textarea, select',
        ) || [],
      ).filter((element) => element.getClientRects().length > 0);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("keydown", onKeyDown);
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    };
  }, []);
  return (
    <>
      <motion.div
        className="agent-picker-backdrop"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        onMouseDown={(event) => {
          if (event.target === event.currentTarget) onClose();
        }}
      />
      <motion.aside
        ref={panel}
        className="agent-picker-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingId}
        initial={{ x: 24, opacity: 0 }}
        animate={{ x: 0, opacity: 1 }}
        exit={{ x: 24, opacity: 0 }}
      >
        <header className="agent-picker-header">
          <div>
            <span className="eyebrow">ÉQUIPE</span>
            <h2 id={headingId}>Choisir un agent</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label="Fermer la liste des agents"
          >
            ×
          </button>
        </header>
        <p className="agent-picker-intro">
          Ouvrez une conversation pour voir le contexte et envoyer une
          indication au bon agent.
        </p>
        <div className="agent-picker-list">
          {agents.map((agent, index) => (
            <button
              type="button"
              className="agent-picker-item"
              key={agent.id}
              onClick={() => {
                onAgent(agent);
                onClose();
              }}
            >
              <AgentAvatar
                agent={agent}
                task={task}
                index={index}
                onClick={() => {}}
                interactive={false}
              />
              <span className="agent-picker-copy">
                <strong>{agent.name}</strong>
                <small>{agent.role}</small>
              </span>
              <span className={`agent-picker-status is-${task ? agentDisplayState(task, agent).status : agent.status}`}>
                {task ? agentDisplayState(task, agent).label : statusLabels[agent.status]}
              </span>
            </button>
          ))}
        </div>
      </motion.aside>
    </>
  );
}

export default AgentAvatars;

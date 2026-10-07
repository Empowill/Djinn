import {
  memo,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { CSSProperties } from "react";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpRight,
  Check,
  ChevronDown,
  Copy,
  GitBranch,
  MessageSquare,
  SlidersHorizontal,
  Terminal,
  TriangleAlert,
  X,
  FileText,
} from "lucide-react";
import { motion } from "motion/react";
import { MarkdownBody } from "./markdown-body";
import { VisualizationFrame } from "./visualization-frame";

import type { Agent, Artifact, FlightEvent, Task } from "./types";
import { event as newEvent } from "./data";
import { Orb, agentColor, agentOrbState } from "./visuals";
import "./agent-chat.css";

export type AgentChatProps = {
  agent: Agent;
  task: Task;
  onClose: () => void;
  onUpdate: (task: Task) => void;
  onFilter: () => void;
  onSend: (text: string, agentId: string) => Promise<boolean>;
};
type Entry = {
  id: string;
  time: string;
  kind: "assistant" | "user" | "tool" | "error" | "artifact";
  text: string;
  title?: string;
  event?: FlightEvent;
  pending?: boolean;
  receipt?: string;
  artifact?: Artifact;
};
type Instruction = NonNullable<Task["instructions"]>[number] & {
  agentId?: string;
};
const statusLabels = {
  queued: "En attente",
  running: "En cours",
  blocked: "Décision requise",
  done: "Terminé",
  error: "Erreur",
};
const bookkeepingTitles = new Set([
  "Le passage de l’agent est terminé",
  "Mission mise en pause",
  "Provider session started",
  "Provider session completed",
  "Diagnostic du fournisseur",
  "Diagnostic Codex",
  "Périmètre mis à jour",
  "Indication prise en compte",
  "Vous",
]);
const knownProviderNoise = [
  /^Reading additional input from stdin\.{0,3}$/i,
  /\bWARN\s+codex_[\w:.-]+:/i,
];
function isKnownProviderNoise(event: FlightEvent) {
  if (event.type !== "error") return false;
  const detail = String(event.detail || "").trim();
  return knownProviderNoise.some((pattern) => pattern.test(detail));
}
const reducedMotion = () =>
  document.documentElement.dataset.motion === "reduced" ||
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export function entriesFor(task: Task, agent: Agent): Entry[] {
  const entries: Entry[] = [];
  for (const event of task.events) {
    if (event.agentId !== agent.id) continue;
    if (event.title.startsWith("Support disponible :")) {
      const title = event.title.slice("Support disponible :".length).trim();
      const artifact = (task.artifacts || []).find(
        (a) =>
          a.title === title &&
          (!event.stepId || a.stepId === event.stepId) &&
          (!event.runId || a.runId === event.runId),
      );
      if (artifact)
        entries.push({
          id: `event-${event.id}`,
          time: event.time,
          kind: "artifact",
          text: title,
          artifact,
        });
      continue;
    }
    if (isKnownProviderNoise(event)) continue;
    if (event.type === "tool" || event.type === "error")
      entries.push({
        id: `event-${event.id}`,
        time: event.time,
        kind: event.type,
        text: event.detail,
        title: event.title,
        event,
      });
    else if (
      (event.type === "note" || event.type === "review") &&
      event.actor !== "human" &&
      !event.lifecycle &&
      !bookkeepingTitles.has(event.title) &&
      !/^Indication (transmise|remise|empêchée|adressée)|^Transmission empêchée/.test(
        event.title,
      ) &&
      !event.title.startsWith("Support disponible :")
    ) {
      entries.push({
        id: `event-${event.id}`,
        time: event.time,
        kind: "assistant",
        text: event.detail || event.title,
      });
    } else if (
      event.type === "decision" &&
      task.questions.some(
        (question) =>
          question.agentId === agent.id && question.title === event.title,
      )
    ) {
      entries.push({
        id: `event-${event.id}`,
        time: event.time,
        kind: "assistant",
        text: `**${event.title}**${event.detail ? `\n\n${event.detail}` : ""}`,
      });
    }
  }
  for (const instruction of (task.instructions || []) as Instruction[]) {
    if (
      instruction.agentId === agent.id ||
      (!instruction.agentId && agent.id === "lead")
    )
      entries.push({
        id: `instruction-${instruction.id}`,
        time: instruction.time,
        kind: "user",
        text: instruction.text,
        pending: !instruction.appliedAt,
        receipt:
          instruction.status === "prevented"
            ? `Empêchée : ${instruction.reason || "destinataire indisponible"}`
            : instruction.status === "consumed" || instruction.appliedAt
              ? "Remise au destinataire"
              : instruction.status === "transmitted"
                ? "Transmise"
                : "En attente",
      });
  }
  for (const question of task.questions) {
    if (question.agentId === agent.id && question.answer && question.answeredAt)
      entries.push({
        id: `answer-${question.id}-${question.answeredAt}`,
        time: question.answeredAt,
        kind: "user",
        text: question.answer,
      });
  }
  return entries
    .map((entry, order) => ({ entry, order }))
    .sort(
      (a, b) =>
        Date.parse(a.entry.time) - Date.parse(b.entry.time) ||
        a.order - b.order,
    )
    .map((item) => item.entry);
}

function CopyButton({
  text,
  label = "Copier",
}: {
  text: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setFailed(false);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1800);
    } catch {
      setFailed(true);
    }
  };
  return (
    <button
      type="button"
      className={`ac-copy ${copied ? "is-copied" : ""}`}
      onClick={copy}
      aria-label={copied ? "Copié" : failed ? "Copie impossible" : label}
      title={copied ? "Copié" : failed ? "Copie impossible" : label}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
      {failed && <span role="status">Copie impossible</span>}
    </button>
  );
}
export function ArtifactChatCard({ artifact }: { artifact: Artifact }) {
  const [open, setOpen] = useState(artifact.type === "visualization");
  return (
    <section
      className="ac-artifact-card"
      aria-label={`Support : ${artifact.title}`}
    >
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)}>
        <FileText size={18} />
        <span>
          <strong>{artifact.title}</strong>
          <small>
            {artifact.type === "visualization"
              ? "Visualisation interactive"
              : artifact.type === "document"
                ? "Document Markdown"
                : "Support"}{" "}
            · révision {artifact.revision || 1}
          </small>
        </span>
        <ChevronDown size={14} />
      </button>
      {open && (
        <div className="ac-artifact-body">
          {artifact.type === "visualization" ? (
            <VisualizationFrame artifact={artifact} />
          ) : artifact.type === "document" ? (
            <MarkdownBody text={artifact.content} />
          ) : (
            <pre>
              {artifact.type === "screenshot"
                ? "Capture disponible dans les supports de l’étape."
                : artifact.content}
            </pre>
          )}
        </div>
      )}
    </section>
  );
}
export { MarkdownBody };

function ToolOutput({ text }: { text: string }) {
  if (!text) return <p className="ac-tool-empty">Aucun détail rapporté.</p>;
  const pattern = /^(Entrée|Commande|Sortie|Code de sortie) :\s*/gm;
  const markers = [...text.matchAll(pattern)];
  if (!markers.length) return <pre className="ac-tool-output">{text}</pre>;
  const sections = markers.map((marker, index) => ({
    label: marker[1],
    text: text
      .slice(
        marker.index! + marker[0].length,
        markers[index + 1]?.index ?? text.length,
      )
      .trimEnd(),
  }));
  const prefix = text.slice(0, markers[0].index).trim();
  return (
    <>
      {prefix && <pre className="ac-tool-output">{prefix}</pre>}
      {sections.map((section, index) => (
        <div className="ac-tool-section" key={index}>
          <div className="ac-tool-section-heading">
            <span>{section.label}</span>
            <CopyButton
              text={section.text}
              label={`Copier : ${section.label}`}
            />
          </div>
          <pre className="ac-tool-output">{section.text}</pre>
        </div>
      ))}
    </>
  );
}

export function AgentChat({
  agent,
  task,
  onClose,
  onUpdate,
  onFilter,
  onSend,
}: AgentChatProps) {
  const headingId = useId();
  const composerId = useId();
  const panel = useRef<HTMLElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const composer = useRef<HTMLTextAreaElement>(null);
  const pinned = useRef(true);
  const autoScrolling = useRef(false);
  const scrollTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const sendLock = useRef(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const [draft, setDraft] = useState("");
  const [pending, setPending] = useState(false);
  const [sendError, setSendError] = useState("");
  const [atEnd, setAtEnd] = useState(true);
  const [contextOpen, setContextOpen] = useState(false);
  const [scope, setScope] = useState(agent.prompt || "");
  const entries = useMemo(
    () => entriesFor(task, agent),
    [task.events, task.instructions, task.questions, task.artifacts, agent.id],
  );
  const lastEntry = entries[entries.length - 1];
  const index = task.agents.findIndex((a) => a.id === agent.id);
  const color = agentColor(agent.id, index);
  const latestBranch = [...task.events]
    .reverse()
    .find((e) => e.agentId === agent.id && (e.branch || e.worktree));
  const branch = agent.branch || latestBranch?.branch;
  const worktree = agent.worktree || latestBranch?.worktree;
  const scrollToEnd = (smooth = true) => {
    const el = viewport.current;
    if (!el) return;
    pinned.current = true;
    setAtEnd(true);
    autoScrolling.current = smooth && !reducedMotion();
    el.scrollTo({
      top: el.scrollHeight,
      behavior: autoScrolling.current ? "smooth" : "auto",
    });
    clearTimeout(scrollTimer.current);
    scrollTimer.current = setTimeout(() => {
      autoScrolling.current = false;
    }, 450);
  };
  useEffect(() => {
    setScope(agent.prompt || "");
  }, [agent.id, agent.prompt]);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    composer.current?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(
        panel.current?.querySelectorAll<HTMLElement>(
          'button,textarea,input,a[href],summary,[tabindex="0"]',
        ) || [],
      ).filter(
        (el) => !el.hasAttribute("disabled") && el.getClientRects().length > 0,
      );
      const first = focusable[0],
        last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      }
      if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener("keydown", keydown);
    return () => {
      document.removeEventListener("keydown", keydown);
      clearTimeout(scrollTimer.current);
      previous?.focus();
    };
  }, [agent.id]);
  useLayoutEffect(() => {
    pinned.current = true;
    scrollToEnd(false);
  }, [agent.id]);
  useLayoutEffect(() => {
    if (pinned.current) scrollToEnd(true);
  }, [entries.length, lastEntry?.id, lastEntry?.text]);
  useEffect(() => {
    const observer = new ResizeObserver(() => {
      if (pinned.current && !autoScrolling.current) scrollToEnd(false);
    });
    if (content.current) observer.observe(content.current);
    if (viewport.current) observer.observe(viewport.current);
    return () => observer.disconnect();
  }, [agent.id]);
  useLayoutEffect(() => {
    const el = composer.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(152, Math.max(44, el.scrollHeight))}px`;
  }, [draft]);
  const send = async () => {
    const text = draft.trim();
    if (!text || sendLock.current) return;
    sendLock.current = true;
    setPending(true);
    setSendError("");
    try {
      if (await onSend(text, agent.id)) {
        setDraft("");
        scrollToEnd(true);
      } else
        setSendError(
          "Le message n’a pas été envoyé. Votre texte est conservé.",
        );
    } catch (error) {
      setSendError(
        error instanceof Error
          ? error.message
          : "Le message n’a pas été envoyé.",
      );
    } finally {
      sendLock.current = false;
      setPending(false);
      composer.current?.focus();
    }
  };
  return (
    <motion.aside
      ref={panel}
      className="agent-chat"
      role="dialog"
      aria-modal="true"
      aria-labelledby={headingId}
      style={{ "--ac-agent-color": color } as CSSProperties}
      initial={{ x: 35, opacity: 0 }}
      animate={{ x: 0, opacity: 1 }}
      exit={{ x: 35, opacity: 0 }}
      transition={{ duration: reducedMotion() ? 0 : 0.18 }}
    >
      <header className="ac-header">
        <Orb
          status={agent.status}
          size={42}
          color={color}
          animation={agentOrbState(agent.id, index)}
        />
        <div className="ac-heading">
          <h2 id={headingId}>{agent.name}</h2>
          <p>
            <span>{agent.model || (agent.origin === "codex" ? "Modèle non communiqué" : task.model || task.provider)}</span>
            {agent.origin === "codex" && <span>Agent Codex observé</span>}
            <span className={`ac-status is-${agent.status}`}>
              {agent.origin === "codex" && agent.live === false ? `Dernier état : ${statusLabels[agent.status]}` : statusLabels[agent.status]}
            </span>
          </p>
        </div>
        <div className="ac-header-actions">
          <button
            className={`ac-icon-button ${contextOpen ? "is-active" : ""}`}
            onClick={() => setContextOpen(!contextOpen)}
            aria-label="Périmètre de l’agent"
            aria-expanded={contextOpen}
          >
            <SlidersHorizontal size={16} />
          </button>
          <button
            className="ac-icon-button"
            onClick={onFilter}
            aria-label="Ouvrir la timeline de la mission"
            title="Timeline"
          >
            <ArrowUpRight size={16} />
          </button>
          <button
            className="ac-icon-button"
            onClick={onClose}
            aria-label="Fermer la conversation"
          >
            <X size={18} />
          </button>
        </div>
      </header>
      {(branch || worktree) && (
        <div className="ac-branch">
          <GitBranch size={12} />
          <span title={worktree}>{branch || worktree}</span>
          {branch && worktree && (
            <small title={worktree}>
              {worktree.split(/[\\/]/).filter(Boolean).pop()}
            </small>
          )}
        </div>
      )}
      {contextOpen && (
        <section className="ac-context">
          <div>
            <strong>{agent.role}</strong>
            <p>{agent.summary}</p>
          </div>
          {agent.id !== "lead" && (
            <>
              <label>
                Périmètre
                <textarea
                  value={scope}
                  onChange={(e) => setScope(e.target.value)}
                  rows={3}
                  disabled={!!task.runId}
                />
              </label>
              <button
                className="ac-context-save"
                disabled={!!task.runId || scope === (agent.prompt || "")}
                onClick={() =>
                  onUpdate({
                    ...task,
                    agents: task.agents.map((a) =>
                      a.id === agent.id ? { ...a, prompt: scope } : a,
                    ),
                    events: [
                      ...task.events,
                      newEvent("note", "Périmètre mis à jour", "", agent.id),
                    ],
                  })
                }
              >
                <Check size={12} />
                Enregistrer
              </button>
            </>
          )}
        </section>
      )}
      <div className="ac-message-region">
        <div
          ref={viewport}
          className="ac-messages"
          onPointerDown={() => {
            autoScrolling.current = false;
          }}
          onKeyDown={(e) => {
            if (["ArrowUp", "PageUp", "Home"].includes(e.key)) {
              autoScrolling.current = false;
              pinned.current = false;
            }
          }}
          onWheel={(e) => {
            if (e.deltaY < 0) {
              autoScrolling.current = false;
              pinned.current = false;
            }
          }}
          onScroll={(e) => {
            const el = e.currentTarget;
            const near = el.scrollHeight - el.scrollTop - el.clientHeight < 72;
            if (near || !autoScrolling.current) {
              pinned.current = near;
              setAtEnd(near);
            }
          }}
        >
          <div
            ref={content}
            className="ac-message-list"
            role="log"
            aria-label={`Conversation avec ${agent.name}`}
            aria-live="off"
          >
            {!entries.length && (
              <div className="ac-empty">
                <MessageSquare size={22} />
                <p>Aucun message de {agent.name}.</p>
              </div>
            )}
            {entries.map((entry) =>
              entry.kind === "artifact" && entry.artifact ? (
                <ArtifactChatCard key={entry.id} artifact={entry.artifact} />
              ) : entry.kind === "tool" ? (
                <details
                  key={entry.id}
                  className="ac-tool"
                  onToggle={() => {
                    const el = viewport.current;
                    if (el) {
                      autoScrolling.current = false;
                      pinned.current =
                        el.scrollHeight - el.scrollTop - el.clientHeight < 72;
                      setAtEnd(pinned.current);
                    }
                  }}
                >
                  <summary>
                    <Terminal size={13} />
                    <span>{entry.title || "Action de l’agent"}</span>
                    <ChevronDown size={12} />
                  </summary>
                  <div className="ac-tool-body">
                    <ToolOutput text={entry.text} />
                  </div>
                </details>
              ) : entry.kind === "error" ? (
                <div key={entry.id} className="ac-error-message">
                  <TriangleAlert size={15} />
                  <div>
                    <strong>{entry.title}</strong>
                    <pre>{entry.text}</pre>
                  </div>
                </div>
              ) : (
                <article
                  key={entry.id}
                  className={`ac-message ac-message-${entry.kind}`}
                  aria-label={
                    entry.kind === "user"
                      ? "Message de vous"
                      : `Message de ${agent.name}`
                  }
                >
                  {entry.kind === "user" ? (
                    <>
                      <span className="ac-user-label">Vous</span>
                      <div className="ac-user-bubble">{entry.text}</div>
                      {entry.pending !== undefined && (
                        <span className="ac-user-delivery">
                          {entry.receipt ||
                            (entry.pending
                              ? "En attente"
                              : "Remise au destinataire")}
                        </span>
                      )}
                    </>
                  ) : (
                    <MarkdownBody text={entry.text} />
                  )}
                  <div className="ac-message-actions">
                    <CopyButton text={entry.text} label="Copier le message" />
                  </div>
                </article>
              ),
            )}
          </div>
        </div>
        {!atEnd && (
          <button
            className="ac-jump"
            onClick={() => scrollToEnd()}
            aria-label="Revenir aux derniers messages"
          >
            <ArrowDown size={14} />
            Derniers messages
          </button>
        )}
      </div>
      {agent.origin === "codex" ? (
        <p className="ac-composer">Conversation Codex observée. Adressez vos indications au chef.</p>
      ) : <form
        className="ac-composer"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        {sendError && (
          <p className="ac-send-error" role="alert">
            {sendError}
          </p>
        )}
        <div className={`ac-composer-box ${pending ? "is-pending" : ""}`}>
          <label htmlFor={composerId} className="ac-sr-only">
            Message à {agent.name}
          </label>
          <textarea
            id={composerId}
            ref={composer}
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value);
              setSendError("");
            }}
            placeholder={`Message à ${agent.name}…`}
            rows={1}
            maxLength={12000}
            disabled={pending}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault();
                void send();
              }
            }}
          />
          <div className="ac-composer-footer">
            <span>
              Entrée pour envoyer<span> · </span>⇧ Entrée pour une nouvelle
              ligne
            </span>
            <button
              type="submit"
              disabled={!draft.trim() || pending}
              className="ac-send"
              aria-label={
                pending ? "Envoi en cours" : `Envoyer à ${agent.name}`
              }
              title="Envoyer"
            >
              {pending ? (
                <span className="ac-send-spinner" />
              ) : (
                <ArrowUp size={16} />
              )}
            </button>
          </div>
        </div>
      </form>}
    </motion.aside>
  );
}
export const AgentDrawer = AgentChat;
export default AgentChat;

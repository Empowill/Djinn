import { agentDisplayState } from "./agent-state";
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
import { t } from "./i18n";

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
  queued: t("chat.status_queued"),
  running: t("chat.status_running"),
  blocked: t("chat.status_blocked"),
  done: t("chat.status_done"),
  error: t("chat.status_error"),
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
  // The same titles in the page's language: the French ones above stay for journals already stored.
  t("chat.event_agent_done"),
  t("chat.event_wish_paused"),
  t("chat.event_provider_diagnostic"),
  t("chat.event_codex_diagnostic"),
  t("chat.event_scope_updated"),
  t("chat.event_instruction_applied"),
  t("chat.you"),
]);
// Event titles that announce an artifact, a delivered instruction or a tool section, in French
// (journals already stored) and in the page's language.
const artifactEventPrefixes = [
  "Support disponible :",
  t("chat.event_artifact_available", { title: "" }).trim(),
];
function artifactEventTitle(title: string): string | undefined {
  const prefix = artifactEventPrefixes.find((p) => title.startsWith(p));
  return prefix === undefined ? undefined : title.slice(prefix.length).trim();
}
const instructionEventTitles = [
  t("chat.event_instruction_sent"),
  t("chat.event_instruction_delivered"),
  t("chat.event_instruction_prevented"),
];
const escapeRegExp = (text: string) =>
  text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const toolSectionPattern = new RegExp(
  `^(?:(Entrée|Commande|Sortie|Code de sortie) :|(${[
    t("chat.tool_input"),
    t("chat.tool_command"),
    t("chat.tool_output"),
    t("chat.tool_exit_code"),
  ]
    .map(escapeRegExp)
    .join("|")}) ?:)\\s*`,
  "gm",
);
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
    const artifactTitle = artifactEventTitle(event.title);
    if (artifactTitle !== undefined) {
      const title = artifactTitle;
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
      !instructionEventTitles.some((title) => event.title.startsWith(title)) &&
      artifactEventTitle(event.title) === undefined
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
            ? t("chat.receipt_prevented", {
                reason: instruction.reason || t("chat.receipt_no_recipient"),
              })
            : instruction.status === "consumed" || instruction.appliedAt
              ? t("chat.receipt_delivered")
              : instruction.status === "transmitted"
                ? t("chat.receipt_transmitted")
                : t("chat.receipt_waiting"),
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
  label = t("chat.copy"),
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
      aria-label={
        copied ? t("chat.copied") : failed ? t("chat.copy_failed") : label
      }
      title={copied ? t("chat.copied") : failed ? t("chat.copy_failed") : label}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
      {failed && <span role="status">{t("chat.copy_failed")}</span>}
    </button>
  );
}
export function ArtifactChatCard({ artifact }: { artifact: Artifact }) {
  const [open, setOpen] = useState(artifact.type === "visualization");
  return (
    <section
      className="ac-artifact-card"
      aria-label={t("chat.artifact_label", { title: artifact.title })}
    >
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)}>
        <FileText size={18} />
        <span>
          <strong>{artifact.title}</strong>
          <small>
            {artifact.type === "visualization"
              ? t("chat.artifact_visualization")
              : artifact.type === "document"
                ? t("chat.artifact_document")
                : t("chat.artifact_generic")}{" "}
            ·{" "}
            {t("chat.artifact_revision", { revision: artifact.revision || 1 })}
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
                ? t("chat.screenshot_in_step")
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
  if (!text) return <p className="ac-tool-empty">{t("chat.tool_empty")}</p>;
  const markers = [...text.matchAll(toolSectionPattern)];
  if (!markers.length) return <pre className="ac-tool-output">{text}</pre>;
  const sections = markers.map((marker, index) => ({
    label: marker[1] ?? marker[2],
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
              label={t("chat.copy_section", { label: section.label })}
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
  const display = agentDisplayState(task, agent);
  const capability = display.permission
    ? t("chat.capability_permission")
    : display.question
      ? t("chat.capability_question")
      : agent.readOnly === true
        ? t("chat.capability_read_only")
        : agent.id === "lead" && task.activity?.lead === "supervises"
          ? t("chat.capability_supervises")
          : agent.origin === "codex"
            ? t("chat.capability_observed")
            : t("chat.capability_instructions");
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
      } else setSendError(t("chat.send_failed_kept"));
    } catch (error) {
      setSendError(
        error instanceof Error ? error.message : t("chat.send_failed"),
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
          status={display.status}
          size={42}
          color={color}
          animation={agentOrbState(agent.id, index)}
        />
        <div className="ac-heading">
          <h2 id={headingId}>{agent.name}</h2>
          <p>
            <span>
              {agent.model ||
                (agent.origin === "codex"
                  ? t("chat.model_unknown")
                  : task.model || task.provider)}
            </span>
            {agent.origin === "codex" && (
              <span>{t("chat.codex_observed")}</span>
            )}
            <span className="ac-capability">{capability}</span>
            <span className={`ac-status is-${display.status}`}>
              {agent.origin === "codex" &&
              agent.live === false &&
              !display.permission
                ? t("chat.last_state", { state: statusLabels[agent.status] })
                : display.label}
            </span>
          </p>
        </div>
        <div className="ac-header-actions">
          <button
            className={`ac-icon-button ${contextOpen ? "is-active" : ""}`}
            onClick={() => setContextOpen(!contextOpen)}
            aria-label={t("chat.agent_scope")}
            aria-expanded={contextOpen}
          >
            <SlidersHorizontal size={16} />
          </button>
          <button
            className="ac-icon-button"
            onClick={onFilter}
            aria-label={t("chat.open_timeline")}
            title={t("chat.timeline")}
          >
            <ArrowUpRight size={16} />
          </button>
          <button
            className="ac-icon-button"
            onClick={onClose}
            aria-label={t("chat.close")}
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
              <p className="ac-capability-note">
                {agent.readOnly
                  ? t("chat.scope_read_only")
                  : t("chat.scope_hint")}
              </p>
              <label>
                {t("chat.scope")}
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
                      newEvent(
                        "note",
                        t("chat.event_scope_updated"),
                        "",
                        agent.id,
                      ),
                    ],
                  })
                }
              >
                <Check size={12} />
                {t("common.save")}
              </button>
            </>
          )}
        </section>
      )}
      {display.permission && (
        <button
          className="button secondary small"
          style={{ margin: "8px 18px" }}
          onClick={() =>
            window.dispatchEvent(
              new CustomEvent("djinn:permission-focus", {
                detail: { taskId: task.id, requestId: display.permission!.id },
              }),
            )
          }
        >
          {t("chat.permission_awaited", { title: display.permission.title })}
        </button>
      )}
      {display.question && !display.permission && (
        <button
          className="button secondary small"
          style={{ margin: "8px 18px" }}
          onClick={() =>
            window.dispatchEvent(
              new CustomEvent("djinn:question-focus", {
                detail: { taskId: task.id, questionId: display.question!.id },
              }),
            )
          }
        >
          {t("chat.answer_question", { title: display.question.title })}
        </button>
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
            aria-label={t("chat.conversation_with", { name: agent.name })}
            aria-live="off"
          >
            {!entries.length && (
              <div className="ac-empty">
                <MessageSquare size={22} />
                <p>{t("chat.empty", { name: agent.name })}</p>
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
                    <span>{entry.title || t("chat.agent_action")}</span>
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
                      ? t("chat.message_from_you")
                      : t("chat.message_from", { name: agent.name })
                  }
                >
                  {entry.kind === "user" ? (
                    <>
                      <span className="ac-user-label">{t("chat.you")}</span>
                      <div className="ac-user-bubble">{entry.text}</div>
                      {entry.pending !== undefined && (
                        <span className="ac-user-delivery">
                          {entry.receipt ||
                            (entry.pending
                              ? t("chat.receipt_waiting")
                              : t("chat.receipt_received"))}
                        </span>
                      )}
                    </>
                  ) : (
                    <MarkdownBody text={entry.text} />
                  )}
                  <div className="ac-message-actions">
                    <CopyButton
                      text={entry.text}
                      label={t("chat.copy_message")}
                    />
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
            aria-label={t("chat.jump_latest")}
          >
            <ArrowDown size={14} />
            {t("chat.latest")}
          </button>
        )}
      </div>
      {agent.origin === "codex" ? (
        <p className="ac-composer">{t("chat.codex_observed_hint")}</p>
      ) : (
        <form
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
              {t("chat.message_to", { name: agent.name })}
            </label>
            <textarea
              id={composerId}
              ref={composer}
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value);
                setSendError("");
              }}
              placeholder={t("chat.message_to_placeholder", {
                name: agent.name,
              })}
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
                {t("chat.hint_send")}
                <span> · </span>
                {t("chat.hint_newline")}
              </span>
              <button
                type="submit"
                disabled={!draft.trim() || pending}
                className="ac-send"
                aria-label={
                  pending
                    ? t("chat.sending")
                    : t("chat.send_to", { name: agent.name })
                }
                title={t("common.send")}
              >
                {pending ? (
                  <span className="ac-send-spinner" />
                ) : (
                  <ArrowUp size={16} />
                )}
              </button>
            </div>
          </div>
        </form>
      )}
    </motion.aside>
  );
}
export const AgentDrawer = AgentChat;
export default AgentChat;

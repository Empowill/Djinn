import { useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  ArrowUpRight,
  ArrowRight,
  ChevronDown,
  Check,
  Clock3,
  CircleHelp,
  GitBranch,
  MoreHorizontal,
  RotateCcw,
  Activity,
  FileText,
  Terminal,
  MessageSquare,
  AlertCircle,
  CheckCircle2,
  Filter,
  Search,
  CornerDownRight,
  LockKeyhole,
  Play,
  Pause,
  X,
} from "lucide-react";
import type { Task, Question, Agent, FlightEvent } from "./types";
import { phaseLabels } from "./data";
import { Orb, Machine, Wave, agentColor, agentOrbState } from "./visuals";
import { getNextRunMode } from "./use-djinn";

type AgentScheduling = Agent & {
  writeScope?: string[];
  dependsOn?: string[];
  readOnly?: boolean;
  waitReason?: string;
  waitingForAgentIds?: string[];
};

const scheduling = (agent: Agent) => agent as AgentScheduling;

const waitReasonLabels: Record<string, string> = {
  concurrency_limit: "Plafond de workers atteint",
  dependency: "Dépendance non satisfaite",
  awaiting_dependency: "Dépendance non satisfaite",
  worker_dependency: "Dépendance non satisfaite",
  write_conflict: "Conflit de périmètre d’écriture",
  scope_conflict: "Conflit de périmètre d’écriture",
  ownership_conflict: "Conflit de propriété d’écriture",
};

const namesFor = (ids: string[] | undefined, agents: Agent[]) =>
  (ids || [])
    .map((id) => agents.find((candidate) => candidate.id === id)?.name || id)
    .filter(Boolean);

export type AgentWaitSummary = {
  title: string;
  detail?: string;
};

export function agentWaitSummary(
  agent: Agent,
  agents: Agent[],
  events: FlightEvent[] = [],
  activeRun = false,
): AgentWaitSummary | null {
  if (agent.id === "lead") return null;
  const value = scheduling(agent);
  const waiting =
    value.status === "blocked" || (value.status === "queued" && activeRun);
  const waitReason = value.waitReason?.trim().slice(0, 240);
  const waitingFor = namesFor(value.waitingForAgentIds, agents);
  const dependencies = namesFor(value.dependsOn, agents);
  const nativeWaitEvent = [...events]
    .reverse()
    .find(
      (entry) =>
        entry.agentId === agent.id &&
        entry.lifecycle === "blocked" &&
        (entry.actor === "agent" || !!entry.runId),
    );
  const eventReason = nativeWaitEvent?.detail?.trim().slice(0, 240);
  if (!waiting && !waitReason && !eventReason && !waitingFor.length)
    return null;
  const reason = waitReason
    ? waitReasonLabels[waitReason] || waitReason
    : eventReason
      ? eventReason
      : waitingFor.length
        ? "Attend la fin d’un autre worker"
        : dependencies.length
          ? "Dépendance non satisfaite"
          : value.status === "blocked"
            ? "Bloqué · motif natif non communiqué"
            : "En attente · motif natif non communiqué";
  const detail = waitingFor.length
    ? `Attend : ${waitingFor.join(", ")}`
    : dependencies.length
      ? `Dépend de : ${dependencies.join(", ")}`
      : nativeWaitEvent
        ? `Dernier signal : ${nativeWaitEvent.title}`
        : undefined;
  return { title: reason, detail };
}

export function agentScope(agent: Agent): string[] {
  return scheduling(agent).writeScope?.filter(Boolean) || [];
}

export function agentIsReadOnly(agent: Agent): boolean {
  return scheduling(agent).readOnly === true;
}
export const formatTime = (value: string) =>
  new Date(value).toLocaleTimeString("fr-FR", {
    hour: "2-digit",
    minute: "2-digit",
  });
export const statusLabels = {
  queued: "En attente",
  running: "En cours",
  blocked: "Bloqué",
  done: "Terminé",
  error: "Erreur",
};
export function QuestionCard({
  question: q,
  onAnswer,
  onReopen,
  readOnly = false,
}: {
  readOnly?: boolean;
  question: Question;
  onAnswer: (value: string) => void;
  onReopen: () => void;
}) {
  const [expanded, setExpanded] = useState(q.blocking && !q.answer);
  const [choice, setChoice] = useState(q.options[0]?.label || "");
  const [custom, setCustom] = useState("");
  const [customMode, setCustomMode] = useState(false);
  return (
    <motion.article
      layout
      id={`question-${q.id}`}
      className={`question-card ${q.answer ? "answered" : ""} ${q.blocking && !q.answer ? "blocking" : ""}`}
      initial={{ opacity: 0, y: 14 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: 30 }}
      transition={{ duration: 0.35 }}
    >
      <button
        className="question-heading"
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
      >
        <span className="question-id">
          {q.answer ? <Check size={14} /> : q.id}
        </span>
        <div>
          <span className="question-meta">
            {q.theme || "Mission"} <span>·</span>{" "}
            {q.answer
              ? "Décision enregistrée"
              : q.blocking
                ? "Votre réponse débloque la suite"
                : "Peut attendre"}
          </span>
          <h3>{q.title}</h3>
          {q.answer && <p className="answer-value">{q.answer}</p>}
        </div>
        <ChevronDown size={16} className={expanded ? "rotated" : ""} />
      </button>
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div
            className="question-content"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.3 }}
          >
            <div className="question-inner">
              <p className="question-context">{q.context}</p>
              {q.answer ? (
                <div className="answered-info">
                  <span>
                    <CheckCircle2 size={15} /> Décision du{" "}
                    {new Date(q.answeredAt || Date.now()).toLocaleDateString(
                      "fr-FR",
                    )}{" "}
                    à {formatTime(q.answeredAt || new Date().toISOString())}
                  </span>
                  <button
                    className="text-button"
                    disabled={readOnly}
                    onClick={onReopen}
                  >
                    <RotateCcw size={13} />
                    Revoir la décision
                  </button>
                </div>
              ) : (
                <>
                  {q.recommendation && (
                    <div className="recommendation">
                      <span className="mini-spark">✳</span>
                      <div>
                        <span>Recommandation</span>
                        <p>{q.recommendation}</p>
                      </div>
                    </div>
                  )}
                  <div className="question-options">
                    {q.options.map((o, i) => (
                      <button
                        key={o.id}
                        disabled={readOnly}
                        className={`option ${choice === o.label && !customMode ? "selected" : ""}`}
                        onClick={() => {
                          setChoice(o.label);
                          setCustomMode(false);
                        }}
                        aria-pressed={choice === o.label && !customMode}
                      >
                        <span className="option-radio">
                          {choice === o.label && !customMode && <span />}
                        </span>
                        <div>
                          <strong>
                            {o.label}
                            {i === 0 && <small>Recommandé</small>}
                          </strong>
                          <p>{o.description}</p>
                        </div>
                      </button>
                    ))}
                  </div>
                  {customMode ? (
                    <textarea
                      autoFocus
                      value={custom}
                      disabled={readOnly}
                      onChange={(e) => setCustom(e.target.value)}
                      placeholder="Précisez votre choix et ce qu’il change…"
                      rows={3}
                      aria-label="Votre réponse personnalisée"
                    />
                  ) : (
                    <button
                      className="text-button custom-answer"
                      disabled={readOnly}
                      onClick={() => setCustomMode(true)}
                    >
                      <MessageSquare size={13} />
                      J’ai une autre idée
                    </button>
                  )}
                  <div className="question-footer">
                    <span>
                      <CornerDownRight size={14} />
                      {q.unlocks ||
                        "Cette réponse rejoindra le contexte de la mission."}
                    </span>
                    <button
                      className="button accent small"
                      disabled={
                        readOnly || (customMode ? !custom.trim() : !choice)
                      }
                      onClick={() =>
                        onAnswer(customMode ? custom.trim() : choice)
                      }
                    >
                      Valider ce choix
                      <ArrowRight size={14} />
                    </button>
                  </div>
                </>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.article>
  );
}
export function MissionHeader({
  task,
  providerControl,
}: {
  task: Task;
  providerControl?: ReactNode;
}) {
  const activeStep = task.steps?.find((step) => step.id === task.activeStepId);
  return (
    <div className="hero mission-header">
      <div className="hero-copy">
        <h1>
          {task.title.split("\n").map((s, i) => (
            <span key={i}>
              {s}
              {i < task.title.split("\n").length - 1 && <br />}
            </span>
          ))}
        </h1>
        <p>
          {task.brief ||
            "Décrivez votre intention. Djinn en fera un plan de travail partagé."}
        </p>
        {activeStep?.objective && (
          <p className="step-objective">
            <strong>{activeStep.title}</strong> · {activeStep.objective}
          </p>
        )}
        <div className="hero-meta">
          <span>
            <GitBranch size={13} />
            {task.project ? task.project.split("/").pop() : "Mission d’exemple"}
          </span>
          <span>
            <Clock3 size={13} />
            {new Date(task.createdAt).toLocaleDateString("fr-FR", {
              day: "numeric",
              month: "short",
            })}
          </span>
          {providerControl || (
            <span className="badge muted">
              {task.demo
                ? "Exploration interactive"
                : task.provider === "codex"
                  ? "Codex"
                  : "Claude Code"}
            </span>
          )}
        </div>
      </div>
      <div className="hero-visual">
        <Machine active={task.status === "running"} />
      </div>
    </div>
  );
}
export function Overview({
  task,
  onAnswer,
  onReopen,
  onAgent,
  onTab,
  onDemo,
  readOnly = false,
  actions,
}: {
  task: Task;
  onAnswer: (id: string, value: string) => void;
  onReopen: (id: string) => void;
  onAgent: (agent: Agent) => void;
  onTab: (tab: string) => void;
  onDemo: () => void;
  readOnly?: boolean;
  actions?: ReactNode;
}) {
  const open = task.questions.filter((q) => !q.answer);
  const answered = task.questions.filter((q) => q.answer);
  const [history, setHistory] = useState(false);
  const active = task.agents.filter((a) => a.status === "running").length;
  const waiting = task.agents.filter((a) =>
    agentWaitSummary(a, task.agents, task.events, !!task.runId),
  );
  const limit = Math.max(1, task.configuration.concurrency || 1);
  return (
    <div className="overview-content">
      <section className="team-section">
        <div className="section-heading">
          <div>
            <h2>
              Agents<span className="count">{task.agents.length}</span>
            </h2>
            <p className="team-caption">
              Jusqu’à {limit} worker{limit > 1 ? "s" : ""} en parallèle si les
              périmètres d’écriture sont disjoints. Un périmètre absent reste
              réservé par prudence.
            </p>
          </div>
          <button className="text-button" onClick={() => onTab("timeline")}>
            Voir la timeline
            <ArrowUpRight size={14} />
          </button>
        </div>
        <div className="agent-grid">
          {task.agents.map((a) =>
            (() => {
              const wait = agentWaitSummary(
                a,
                task.agents,
                task.events,
                !!task.runId,
              );
              const scope = agentScope(a);
              const isReadOnly =
                a.id === "lead"
                  ? task.activity?.lead !== "integrates"
                  : agentIsReadOnly(a);
              const leadSupervising =
                a.id === "lead" &&
                !readOnly &&
                task.activity?.lead === "supervises" &&
                task.activity.activeAgents.some((agent) => agent.id !== "lead");
              const leadIntegrating =
                a.id === "lead" && task.activity?.lead === "integrates";
              return (
                <button
                  className={`agent-card ${a.status} ${wait ? "has-wait" : ""}`}
                  key={a.id}
                  onClick={() => onAgent(a)}
                >
                  <div className="agent-card-top">
                    <Orb
                      status={a.status}
                      size={60}
                      color={agentColor(a.id, task.agents.indexOf(a))}
                      animation={agentOrbState(a.id, task.agents.indexOf(a))}
                    />
                    <ArrowUpRight size={14} />
                  </div>
                  <h3>
                    {a.name}
                    <span
                      className="agent-code"
                      style={{
                        color: agentColor(a.id, task.agents.indexOf(a)),
                      }}
                    >
                      {isReadOnly
                        ? "lecture seule"
                        : a.id === "lead"
                          ? "00"
                          : String(task.agents.indexOf(a)).padStart(2, "0")}
                    </span>
                  </h3>
                  <p className="agent-role">{a.role}</p>
                  {a.origin === "codex" && (
                    <p className="agent-scope" title={a.providerThreadId}>
                      Codex · {a.model || "modèle non communiqué"}
                      {a.parentAgentId &&
                        ` · parent : ${task.agents.find((parent) => parent.id === a.parentAgentId)?.name || a.parentAgentId}`}
                    </p>
                  )}
                  {a.activity && <p className="agent-role">{a.activity}</p>}
                  {scope.length ? (
                    <p className="agent-scope" title={scope.join("\n")}>
                      <span>Périmètre</span> {scope.join(" · ")}
                    </p>
                  ) : (
                    <p className="agent-scope muted">
                      {a.origin === "codex"
                        ? "Périmètre non communiqué par Codex"
                        : isReadOnly
                          ? "Lecture seule · aucune écriture"
                          : "Périmètre non déclaré · réservé par prudence"}
                    </p>
                  )}
                  {wait && (
                    <p className="agent-wait" title={wait.detail || wait.title}>
                      <Clock3 size={11} />
                      <span>
                        {wait.title}
                        {wait.detail && <small>{wait.detail}</small>}
                      </span>
                    </p>
                  )}
                  <div className="agent-progress">
                    <span
                      style={{
                        width: `${a.progress}%`,
                        background: agentColor(a.id, task.agents.indexOf(a)),
                      }}
                    />
                  </div>
                  <div className="agent-card-bottom">
                    <span className={`status-dot ${a.status}`} />
                    {a.origin === "codex" && a.live === false
                      ? `Dernier état : ${statusLabels[a.status]}`
                      : leadSupervising
                        ? "Supervise · lecture seule · Attend les sous-agents"
                        : leadIntegrating
                          ? "Intègre après les workers"
                          : isReadOnly
                            ? "Lecture seule"
                            : statusLabels[a.status]}
                    <span>{a.progress}%</span>
                  </div>
                </button>
              );
            })(),
          )}
        </div>
        {waiting.length > 0 && (
          <p className="team-waiting-note">
            {waiting.length} worker{waiting.length > 1 ? "s" : ""} en attente :
            ouvrez sa carte pour voir le motif rapporté et les dépendances.
          </p>
        )}
        {!task.agents.length && (
          <div className="empty-team">
            <Wave />
            <p>Les agents apparaîtront après le cadrage.</p>
          </div>
        )}
      </section>
      <AnimatePresence>
        {open.length > 0 && (
          <motion.div
            key="pending-decisions"
            initial={{ opacity: 0, y: 12, height: 0 }}
            animate={{ opacity: 1, y: 0, height: "auto" }}
            exit={{ opacity: 0, y: -8, height: 0 }}
            transition={{ duration: 0.25 }}
          >
            {" "}
            <section className="decisions-section">
              <div className="section-heading">
                <div>
                  <h2>
                    Décisions<span className="count">{open.length}</span>
                  </h2>
                </div>
                <span
                  className={`status-text ${open.some((q) => q.blocking) ? "warm" : "green"}`}
                >
                  <span className="status-dot" />
                  {open.some((q) => q.blocking)
                    ? "La mission attend votre signal"
                    : open.length
                      ? "Vos agents peuvent avancer"
                      : "Toutes les décisions sont prises"}
                </span>
              </div>
              <AnimatePresence mode="popLayout">
                {open.map((q) => (
                  <QuestionCard
                    key={q.id}
                    question={q}
                    readOnly={readOnly}
                    onAnswer={(v) => onAnswer(q.id, v)}
                    onReopen={() => onReopen(q.id)}
                  />
                ))}
              </AnimatePresence>
            </section>
          </motion.div>
        )}
      </AnimatePresence>
      {answered.length > 0 && (
        <div className="decision-history">
          <button className="text-button" onClick={() => setHistory(!history)}>
            <CheckCircle2 size={14} />
            {answered.length} décision{answered.length > 1 ? "s" : ""}{" "}
            enregistrée{answered.length > 1 ? "s" : ""}
            <ChevronDown size={13} className={history ? "rotated" : ""} />
          </button>
          <AnimatePresence>
            {history && (
              <motion.div
                initial={{ opacity: 0, height: 0 }}
                animate={{ opacity: 1, height: "auto" }}
                exit={{ opacity: 0, height: 0 }}
              >
                {answered.map((q) => (
                  <QuestionCard
                    key={q.id}
                    question={q}
                    readOnly={readOnly}
                    onAnswer={(v) => onAnswer(q.id, v)}
                    onReopen={() => onReopen(q.id)}
                  />
                ))}
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      )}

      {actions}
      <div className="mission-bottom">
        <span>
          <Activity size={13} />
          {readOnly
            ? "Historique de l’étape"
            : `${active} agent${active > 1 ? "s" : ""} en cours`}
        </span>
        <span>
          <FileText size={13} />
          {task.artifacts.length} supports de mission
        </span>
        <span>
          <LockKeyhole size={13} />
          Contexte conservé sur votre machine
        </span>
      </div>
    </div>
  );
}
const eventIcon = {
  note: FileText,
  agent: GitBranch,
  decision: CircleHelp,
  tool: Terminal,
  error: AlertCircle,
  phase: Activity,
  review: MessageSquare,
};
export function EventRow({
  entry,
  task,
  compact = false,
}: {
  entry: FlightEvent;
  task: Task;
  compact?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const Icon = eventIcon[entry.type] || FileText;
  const agent = task.agents.find((a) => a.id === entry.agentId);
  return (
    <div className={`event-row ${entry.type} ${compact ? "compact" : ""}`}>
      <div className="event-line">
        <span
          className="event-node"
          style={
            agent
              ? {
                  color: agentColor(agent.id, task.agents.indexOf(agent)),
                  borderColor: `${agentColor(agent.id, task.agents.indexOf(agent))}55`,
                }
              : undefined
          }
        >
          <Icon size={compact ? 12 : 15} />
        </span>
      </div>
      <div className="event-body">
        <div className="event-metadata">
          <span
            style={
              agent
                ? { color: agentColor(agent.id, task.agents.indexOf(agent)) }
                : undefined
            }
          >
            {agent?.name || "Mission"}
          </span>
          <time>{formatTime(entry.time)}</time>
        </div>
        <button
          className="event-title"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          {entry.title}
          {entry.detail && (
            <ChevronDown size={12} className={open ? "rotated" : ""} />
          )}
        </button>
        {entry.detail &&
          (compact ? (
            <AnimatePresence>
              {open && (
                <motion.p
                  className="event-short"
                  initial={{ opacity: 0, height: 0 }}
                  animate={{ opacity: 1, height: "auto" }}
                  exit={{ opacity: 0, height: 0 }}
                >
                  {entry.detail.slice(0, 350)}
                </motion.p>
              )}
            </AnimatePresence>
          ) : (
            <AnimatePresence>
              {open && (
                <motion.pre
                  className="event-detail"
                  initial={{ height: 0, opacity: 0 }}
                  animate={{ height: "auto", opacity: 1 }}
                  exit={{ height: 0, opacity: 0 }}
                >
                  {entry.detail}
                </motion.pre>
              )}
            </AnimatePresence>
          ))}
      </div>
    </div>
  );
}
export function ActivityRail({
  task,
  onTimeline,
  onAgent,
}: {
  task: Task;
  onTimeline: () => void;
  onAgent: (agent: Agent) => void;
}) {
  const events = [...task.events].reverse().slice(0, 5);
  return (
    <aside className="activity-rail">
      <div className="rail-header">
        <span className="eyebrow">ACTIVITÉ</span>
        <span className="live-indicator">
          <span />
          {task.demo ? "DÉMO" : task.runId ? "LIVE" : "JOURNAL"}
        </span>
      </div>
      <div className="signal-visual">
        <Wave paused={task.status === "paused"} />
      </div>
      <div className="rail-title">
        <h3>Événements</h3>
        <span>{task.events.length}</span>
      </div>
      <div className="rail-events">
        <AnimatePresence initial={false}>
          {events.map((e) => (
            <motion.div
              key={e.id}
              initial={{ opacity: 0, y: -10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.35 }}
            >
              <EventRow entry={e} task={task} compact />
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
      <button className="rail-link" onClick={onTimeline}>
        Timeline complète
        <ArrowUpRight size={14} />
      </button>
      <div className="rail-agents">
        <span className="eyebrow">SUR LE PONT</span>
        {task.agents.slice(0, 4).map((a) => (
          <button onClick={() => onAgent(a)} key={a.id}>
            <Orb
              status={a.status}
              size={26}
              color={agentColor(a.id, task.agents.indexOf(a))}
              animation={agentOrbState(a.id, task.agents.indexOf(a))}
            />
            <span>
              {a.name}
              <small>{a.role}</small>
            </span>
            <span className={`status-dot ${a.status}`} />
          </button>
        ))}
      </div>
    </aside>
  );
}
export { Timeline } from "./temporal-timeline";
export { AgentChat as AgentDrawer } from "./agent-chat";

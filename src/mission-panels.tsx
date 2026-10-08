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
import type {
  Task,
  Question,
  Agent,
  FlightEvent,
  PermissionDecision,
  PermissionRequest,
} from "./types";
import { phaseLabels } from "./data";
import { Orb, Machine, Wave, agentColor, agentOrbState } from "./visuals";
import { getNextRunMode } from "./use-djinn";
import { PermissionPanel } from "./permission-panel";
import { agentStatusLabel } from "./mission-progress";
import { language, t } from "./i18n";

type AgentScheduling = Agent & {
  writeScope?: string[];
  dependsOn?: string[];
  readOnly?: boolean;
  waitReason?: string;
  waitingForAgentIds?: string[];
};

const scheduling = (agent: Agent) => agent as AgentScheduling;

const waitReasonLabels: Record<string, string> = {
  concurrency_limit: t("panels.wait_concurrency_limit"),
  dependency: t("panels.wait_dependency"),
  awaiting_dependency: t("panels.wait_dependency"),
  worker_dependency: t("panels.wait_dependency"),
  write_conflict: t("panels.wait_write_conflict"),
  scope_conflict: t("panels.wait_write_conflict"),
  ownership_conflict: t("panels.wait_ownership_conflict"),
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
        ? t("panels.wait_other_worker")
        : dependencies.length
          ? t("panels.wait_dependency")
          : value.status === "blocked"
            ? t("panels.wait_blocked_unknown")
            : t("panels.wait_queued_unknown");
  const detail = waitingFor.length
    ? t("panels.waits_for", { names: waitingFor.join(", ") })
    : dependencies.length
      ? t("panels.depends_on", { names: dependencies.join(", ") })
      : nativeWaitEvent
        ? t("panels.last_signal", { title: nativeWaitEvent.title })
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
  new Date(value).toLocaleTimeString(language, {
    hour: "2-digit",
    minute: "2-digit",
  });
export const statusLabels = {
  queued: t("panels.status_queued"),
  running: t("panels.status_running"),
  blocked: t("panels.status_blocked"),
  done: t("panels.status_done"),
  error: t("panels.status_error"),
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
  const options = q.options || [];
  const [choice, setChoice] = useState(options[0]?.label || "");
  const [custom, setCustom] = useState("");
  const [customMode, setCustomMode] = useState(options.length === 0);
  const recommendation = q.recommendation?.trim();
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
            {q.theme || t("common.wish")} <span>·</span>{" "}
            {q.answer
              ? t("panels.decision_recorded")
              : q.blocking
                ? t("panels.answer_unblocks")
                : t("panels.can_wait")}
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
                    <CheckCircle2 size={15} />{" "}
                    {t("panels.decided_on", {
                      date: new Date(
                        q.answeredAt || Date.now(),
                      ).toLocaleDateString(language),
                      time: formatTime(
                        q.answeredAt || new Date().toISOString(),
                      ),
                    })}
                  </span>
                  <button
                    className="text-button"
                    disabled={readOnly}
                    onClick={onReopen}
                  >
                    <RotateCcw size={13} />
                    {t("panels.reopen_decision")}
                  </button>
                </div>
              ) : (
                <>
                  {recommendation && (
                    <div className="recommendation">
                      <span className="mini-spark">✳</span>
                      <div>
                        <span>{t("panels.recommendation")}</span>
                        <p>{recommendation}</p>
                      </div>
                    </div>
                  )}
                  {options.length > 0 && (
                    <div className="question-options">
                      {options.map((o) => (
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
                            <strong>{o.label}</strong>
                            <p>{o.description}</p>
                          </div>
                        </button>
                      ))}
                    </div>
                  )}
                  {customMode ? (
                    <textarea
                      autoFocus
                      value={custom}
                      disabled={readOnly}
                      onChange={(e) => setCustom(e.target.value)}
                      placeholder={t("panels.custom_answer_placeholder")}
                      rows={3}
                      aria-label={t("panels.custom_answer_label")}
                    />
                  ) : (
                    <button
                      className="text-button custom-answer"
                      disabled={readOnly}
                      onClick={() => setCustomMode(true)}
                    >
                      <MessageSquare size={13} />
                      {t("panels.other_idea")}
                    </button>
                  )}
                  <div className="question-footer">
                    <span>
                      <CornerDownRight size={14} />
                      {q.unlocks || t("panels.answer_joins_context")}
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
                      {options.length > 0 && !customMode
                        ? t("panels.confirm_choice")
                        : t("panels.confirm_answer")}
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
  const pendingPermission = task.permissions?.find(
    (request) => request.status === "pending",
  );
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
        <p>{task.brief || t("panels.brief_placeholder")}</p>
        {activeStep?.objective && (
          <p className="step-objective">
            <strong>{activeStep.title}</strong> · {activeStep.objective}
          </p>
        )}
        <div className="hero-meta">
          {pendingPermission && (
            <button
              className="button secondary small"
              onClick={() =>
                window.dispatchEvent(
                  new CustomEvent("djinn:permission-focus", {
                    detail: {
                      taskId: task.id,
                      requestId: pendingPermission.id,
                    },
                  }),
                )
              }
            >
              <LockKeyhole size={13} />{" "}
              {t("panels.permission_awaited", {
                agent:
                  pendingPermission.agentName ||
                  task.agents.find(
                    (agent) => agent.id === pendingPermission.agentId,
                  )?.name ||
                  "Agent",
              })}
            </button>
          )}
          <span>
            <GitBranch size={13} />
            {task.project
              ? task.project.split("/").pop()
              : t("panels.sample_wish")}
          </span>
          <span>
            <Clock3 size={13} />
            {new Date(task.createdAt).toLocaleDateString(language, {
              day: "numeric",
              month: "short",
            })}
          </span>
          {providerControl || (
            <span className="badge muted">
              {task.demo
                ? t("panels.interactive_exploration")
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
  workSummary,
  permissions = [],
  onRespondPermission,
  busyPermissionIds = [],
  onSteer,
  onResume,
}: {
  task: Task;
  onAnswer: (id: string, value: string) => void;
  onReopen: (id: string) => void;
  onAgent: (agent: Agent) => void;
  onTab: (tab: string) => void;
  onDemo: () => void;
  readOnly?: boolean;
  actions?: ReactNode;
  workSummary?: ReactNode;
  permissions?: PermissionRequest[];
  onRespondPermission?: (
    request: PermissionRequest,
    decision: PermissionDecision,
    answers?: Record<string, string>,
  ) => Promise<boolean>;
  busyPermissionIds?: string[];
  onSteer?: (value: string) => Promise<boolean>;
  onResume?: () => void;
}) {
  const open = task.questions.filter((q) => !q.answer);
  const answered = task.questions.filter((q) => q.answer);
  const [history, setHistory] = useState(false);
  const active = task.agents.filter((a) => a.status === "running").length;
  const selectedStepId = task.selectedStepId || task.activeStepId;
  const permissionRequests = permissions.filter(
    (request) =>
      request.status === "pending" &&
      (selectedStepId === task.activeStepId ||
        !request.stepId ||
        !selectedStepId ||
        request.stepId === selectedStepId),
  );
  const activeStep = task.steps?.find((step) => step.id === task.activeStepId);
  const currentResult =
    task.stepResult?.stepId === task.activeStepId ? task.stepResult : undefined;
  const passageFailed =
    task.status === "error" || activeStep?.status === "error";
  const latestError = passageFailed
    ? task.events
        .slice()
        .reverse()
        .find(
          (entry) =>
            entry.type === "error" &&
            (!entry.stepId || entry.stepId === task.activeStepId),
        )
    : undefined;
  const stepResult = passageFailed
    ? {
        stepId: task.activeStepId,
        status: "blocked" as const,
        summary: t("panels.run_interrupted"),
        reason: (
          latestError?.detail ||
          latestError?.title ||
          t("panels.run_unfinished")
        ).slice(0, 4000),
        nextAction: t("panels.run_fix_and_resume"),
      }
    : currentResult;
  const resultNeedsAction =
    !!stepResult &&
    stepResult.stepId === task.activeStepId &&
    stepResult.status !== "ready";
  // A wish's documents are no move to make: they show below, out of the action center.
  const hasRestitution =
    !!actions &&
    !task.fromWish &&
    (task.artifacts.length > 0 || (task.actions || []).length > 0);
  const hasActionCenter =
    open.length > 0 ||
    permissionRequests.length > 0 ||
    hasRestitution ||
    resultNeedsAction;
  return (
    <div className="overview-content">
      {hasActionCenter && (
        <section
          className="action-center"
          id="action-center"
          aria-label={t("panels.your_move")}
        >
          <div className="action-center-heading">
            <div>
              <span className="eyebrow">{t("panels.next_action")}</span>
              <h2>{t("panels.your_move")}</h2>
              <p>{t("panels.your_move_detail")}</p>
            </div>
            {open.length > 0 && (
              <span
                className={`status-text ${open.some((q) => q.blocking) ? "warm" : "green"}`}
              >
                <span className="status-dot" />
                {open.some((q) => q.blocking)
                  ? t("panels.answer_required")
                  : t("panels.can_proceed")}
              </span>
            )}
          </div>
          {resultNeedsAction && stepResult && (
            <article className="step-result-action">
              <div>
                <span className="step-result-kicker">
                  {stepResult.status === "blocked"
                    ? t("panels.action_needed")
                    : t("panels.answer_expected")}
                </span>
                <h3>{stepResult.summary}</h3>
                {stepResult.reason && <p>{stepResult.reason}</p>}
                {stepResult.nextAction && (
                  <p className="step-result-next">{stepResult.nextAction}</p>
                )}
              </div>
              {task.runId ? (
                <button
                  type="button"
                  className="button secondary small"
                  onClick={() =>
                    void onSteer?.(
                      t("panels.step_action_requested", {
                        action: stepResult.nextAction || stepResult.summary,
                      }),
                    )
                  }
                >
                  {t("panels.tell_lead")} <ArrowRight size={13} />
                </button>
              ) : (
                <button
                  type="button"
                  className="button accent small"
                  onClick={onResume}
                >
                  {t("panels.resume_step")} <ArrowRight size={13} />
                </button>
              )}
            </article>
          )}
          <AnimatePresence>
            {open.length > 0 && (
              <motion.div
                key="pending-decisions"
                initial={{ opacity: 0, y: 12, height: 0 }}
                animate={{ opacity: 1, y: 0, height: "auto" }}
                exit={{ opacity: 0, y: -8, height: 0 }}
                transition={{ duration: 0.25 }}
              >
                <section className="decisions-section">
                  <div className="section-heading">
                    <h3>
                      {t("panels.decisions")}
                      <span className="count">{open.length}</span>
                    </h3>
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
          {permissionRequests.length > 0 && onRespondPermission && (
            <PermissionPanel
              requests={permissionRequests}
              onRespond={onRespondPermission}
              busyIds={busyPermissionIds}
            />
          )}
          {workSummary}
          {actions}
        </section>
      )}
      {!hasActionCenter && workSummary}
      {!hasActionCenter && task.fromWish && actions}
      {answered.length > 0 && (
        <div className="decision-history">
          <button className="text-button" onClick={() => setHistory(!history)}>
            <CheckCircle2 size={14} />
            {t("panels.decisions_recorded", { count: answered.length })}
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
      <div className="mission-bottom">
        <span>
          <Activity size={13} />
          {readOnly
            ? t("panels.step_history")
            : t("panels.agents_running", { count: active })}
        </span>
        <span>
          <FileText size={13} />
          {t("panels.wish_supports", { count: task.artifacts.length })}
        </span>
        <span>
          <LockKeyhole size={13} />
          {t("panels.context_kept_local")}
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
            {agent?.name || t("common.wish")}
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
        <span className="eyebrow">{t("panels.activity")}</span>
        <span className="live-indicator">
          <span />
          {task.demo
            ? t("panels.live_demo")
            : task.runId
              ? "LIVE"
              : t("panels.live_journal")}
        </span>
      </div>
      <div className="signal-visual">
        <Wave paused={task.status === "paused"} />
      </div>
      <div className="rail-title">
        <h3>{t("panels.events")}</h3>
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
        {t("panels.full_timeline")}
        <ArrowUpRight size={14} />
      </button>
      <div className="rail-agents">
        <span className="eyebrow">{t("panels.on_deck")}</span>
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
              <small>
                {a.role} · {agentStatusLabel(task, a)}
              </small>
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
export { StageReport } from "./mission-progress";

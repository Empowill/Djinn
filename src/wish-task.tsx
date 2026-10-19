// A task of a wish: its code, what it does, where it stands, what it spent, and, opened, its facts, its worker's
// last word and its events as they come (TaskService.Watch), with a box to send the running worker an instruction
// (TaskService.Send). A running worker pauses, and a paused one goes on (TaskService.Pause and Resume). A watcher (a
// command, no agent) shows the first line of its last paragraph. A task that comes from a decision links back to it.
import {
  Activity,
  AlertCircle,
  CheckCheck,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CirclePause,
  CirclePlay,
  CircleStop,
  FileText,
  Flame,
  Gauge,
  Lock,
  MessageSquare,
  MessageSquarePlus,
  Play,
  RotateCcw,
  ScrollText,
  Send,
  Terminal,
  Zap,
} from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  memo,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  Closer,
  HealthState,
  IntegrationState,
  type Project,
  Provider,
  type Task,
  type TaskEvent,
  TaskEventKind,
  TaskStatus,
} from "../gen/ts/plan/v1/plan_pb";
import { jump } from "./attention";
import { type Decision } from "./data/decisions";
import {
  useDjinn,
  useOptionalData,
  useTaskEvents,
  useTaskPrompt,
} from "./data/djinn";
import {
  canUrge,
  isInvestigation,
  isMerge,
  isMockup,
  isUrgent,
} from "./data/flight";
import {
  ifritGates,
  ifritLinks,
  ifritLit,
  ifritOut,
  ifritPhase,
  ifritWaitOf,
  ifritWord,
  isIfrit,
} from "./data/ifrit";
import {
  cancelledText,
  firstLine,
  foldedWaits,
  gitReason,
  liveWaitReason,
  taskActivity,
  taskFinished,
  taskModelDisplay,
  taskStatusText,
  taskTime,
  taskTone,
  usd,
  useNow,
  when,
} from "./data/format";
import { DecisionLink } from "./decision-log";
import { HOURGLASS } from "./hourglass";
import { language, t } from "./i18n";
import { IfritCountdown, IfritGates, IfritLinks, KeepLit } from "./ifrit";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge, ToneIcon } from "./status";
import { type DiffView, TaskReview } from "./diff";
import {
  costTitle,
  resourcesDetail,
  resourcesNow,
  TaskTurns,
  TaskUsage,
  usageDetail,
} from "./usage";

const eventIcons: Partial<Record<TaskEventKind, typeof FileText>> = {
  [TaskEventKind.PROMPT]: MessageSquare,
  [TaskEventKind.TEXT]: FileText,
  [TaskEventKind.TOOL_CALL]: Terminal,
  [TaskEventKind.TOOL_RESULT]: Terminal,
  [TaskEventKind.USAGE]: Gauge,
  [TaskEventKind.STATUS]: Activity,
  [TaskEventKind.ERROR]: AlertCircle,
  [TaskEventKind.GATE]: Lock,
  [TaskEventKind.LOG]: ScrollText,
  [TaskEventKind.MESSAGE]: MessageSquarePlus,
  [TaskEventKind.RECEIVED]: CheckCheck,
};

const agentNames: Record<Provider, string> = {
  [Provider.UNSPECIFIED]: "claude",
  [Provider.CLAUDE]: "claude",
  [Provider.FAKE]: "fake",
  [Provider.CODEX]: "codex",
  [Provider.ANTIGRAVITY]: "antigravity",
  [Provider.WATCH]: "watch",
};

// agentOf names a task's agent, and its model when one was asked: "claude · haiku". A task stored before providers
// ran Claude.
export function agentOf(task: Task): string {
  const name = agentNames[task.provider] ?? "claude";
  return task.model ? `${name} · ${task.model}` : name;
}

// promptPreview returns the first few lines of a prompt, trimmed and truncated with an ellipsis if long.
export function promptPreview(
  prompt: string,
  maxLines = 3,
  maxChars = 240,
): string {
  if (!prompt) return "";
  const trimmed = prompt.trim();
  if (!trimmed) return "";
  const lines = trimmed
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
  if (!lines.length) return "";
  const hasMoreLines = lines.length > maxLines;
  const taken = lines.slice(0, maxLines);
  let text = taken.join("\n");
  const hasMoreChars = text.length > maxChars;
  if (hasMoreChars) {
    text = text.slice(0, maxChars).trimEnd();
  }
  if (hasMoreLines || hasMoreChars) {
    if (!text.endsWith("…") && !text.endsWith("...")) {
      text += "…";
    }
  }
  return text;
}

// TaskPromptFold shows a task's prompt in Markdown, folded after a few lines with a button to show all.
function TaskPromptFold({ prompt }: { prompt: string }) {
  const [expanded, setExpanded] = useState(false);
  if (!prompt.trim()) return null;
  const lines = prompt.split(/\r?\n/).filter(Boolean);
  const isLong = lines.length > 3 || prompt.length > 180;
  return (
    <div className="wish-task-prompt-section">
      <span className="eyebrow">{t("task.prompt")}</span>
      <div
        className={`wish-task-prompt-body ${!expanded && isLong ? "folded" : "expanded"}`}
      >
        <MarkdownBody text={prompt} />
      </div>
      {isLong && (
        <button
          type="button"
          className="text-button wish-task-prompt-toggle"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? t("task.show_less") : t("task.show_all")}
        </button>
      )}
    </div>
  );
}

function isMemoryWait(reason?: string): boolean {
  if (!reason) return false;
  if (reason.startsWith("the machine is under pressure")) return false;
  return (
    reason.includes("commits up to") ||
    reason.includes("peaks at") ||
    reason.includes("waits for memory") ||
    reason.includes("memory")
  );
}

export function isGreenWait(reason?: string): boolean {
  return Boolean(reason && reason.includes("to be green again"));
}

export function compactGreenWait(reason?: string): string {
  if (!reason) return "";
  const idx = reason.indexOf(" (push checks red at ");
  return idx > 0 ? reason.slice(0, idx) : reason;
}

// WaitReason is why a task waits, as Djinn says it; the task it waits for ("waits for W8 to be committed") is a link
// to its card, which stays in sight until its work is committed (settledTask).
function WaitReason({
  reason,
  codes,
}: {
  reason: string;
  codes?: ReadonlyMap<string, string>;
}) {
  const m = /^(.*?waits for )([A-Za-z]+\d+)\b(.*)$/s.exec(reason);
  if (!m) return <>{reason}</>;
  return (
    <>
      {m[1]}
      <TaskLink code={m[2]} codes={codes} />
      {m[3]}
    </>
  );
}

export const WishTask = memo(function WishTask({
  task,
  project,
  codes,
  origin,
  forkedAs = "",
  fixedBy = "",
  decision,
  focused = false,
  onStop,
  onStart,
  onSend,
  onHold,
  onUrge,
  onDecision,
  onDone,
  onRelight,
  merges,
  nested,
  mergeOpen = false,
  alsoMerged,
  onTurnComments,
}: {
  task: Task;
  project?: Project;
  // The codes of the wish's tasks, by id: what this one waits for, what it was forked from.
  codes?: ReadonlyMap<string, string>;
  // Where the task comes from, in the flying carpet of several wishes: its wish.
  origin?: ReactNode;
  // The task that took over this one, cut short: its code.
  forkedAs?: string;
  // The correction task that fixes its failed merge while it works (fixerOf): its code.
  fixedBy?: string;
  // The decision the task comes from, which onDecision shows.
  decision?: Decision;
  // Brought into sight and opened, from its decision.
  focused?: boolean;
  onStop: (task: Task) => void;
  // Starts a task that waits for the machine alone, anyway (TaskService.Start).
  onStart?: (task: Task) => void;
  onSend: (text: string, task: Task) => Promise<unknown>;
  // Pauses the task's worker (true) or lets it go on (false); none where djinn cannot pause one (Windows).
  onHold?: (pause: boolean, task: Task) => void;
  // Marks the task urgent for the developer's quick feedback (true), or takes its urgent mark off (false); none where
  // it cannot be done from here.
  onUrge?: (task: Task, urgent: boolean) => void;
  onDecision?: (task: Task) => void;
  // Marks the task done by hand, with a note; none where the task cannot be closed from here.
  onDone?: (note: string, task: Task) => Promise<unknown>;
  // Lights an ifrit put out again with a message (TaskService.Continue); none where it cannot be done from here.
  onRelight?: (text: string, task: Task) => Promise<unknown>;
  // The merges that bring this task's work in (src/data/flight.ts mergeNest), and their rows: folded under it.
  merges?: readonly Task[];
  nested?: ReactNode;
  // Unfolds them: one of them is brought into sight.
  mergeOpen?: boolean;
  // The merges that bring this work in too but show under another task: a link to that task each.
  alsoMerged?: readonly { merge: Task; host: Task }[];
  // Turns the comments on its diff that went to no worker into work; none where it cannot be done from here.
  onTurnComments?: (task: Task) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(focused);
  const [closing, setClosing] = useState(false);
  const prompt = useTaskPrompt(task);
  const preview = promptPreview(prompt);
  useEffect(() => {
    if (!focused) return;
    setOpen(true);
    document
      .getElementById(`task-${task.id}`)
      ?.scrollIntoView({ block: "center" });
  }, [focused, task.id]);
  const watcher = task.provider === Provider.WATCH;
  const holdable =
    !!onHold &&
    (task.status === TaskStatus.RUNNING || task.status === TaskStatus.PAUSED);
  const paused = task.status === TaskStatus.PAUSED;
  const stoppable =
    task.status === TaskStatus.RUNNING ||
    task.status === TaskStatus.PAUSED ||
    task.status === TaskStatus.PENDING ||
    task.status === TaskStatus.RESUMING;
  const after = taskFinished(task.status)
    ? []
    : (task.dependsOn ?? [])
        .map((id) => codes?.get(id))
        .filter((code): code is string => !!code);
  const tone = taskTone(task, forkedAs, fixedBy);
  // The main counter ticks while it runs, and freezes while paused or done.
  const now = useNow(task.status === TaskStatus.RUNNING && !!task.startTime);
  const time = taskTime(task, now);
  const activity = taskActivity(task, now);
  const work = taskWork(task, codes, fixedBy);
  // An ifrit lit or put out: its links, gates and buttons on a line of their own, Finish in place of Mark done.
  const ifrit = isIfrit(task);
  const lit = ifritLit(task);
  const out = ifritOut(task);
  const phase = ifritPhase(task, now);
  const reply = ifrit ? firstLine(task.ifrit?.reply ?? "") : "";
  const [relighting, setRelighting] = useState(false);
  // The gates it holds while lit, and the gate an ifrit holds that it waits for, read as words: a new reading of the
  // gates draws the card again only when they change.
  const held = useOptionalData(
    (s) => (lit ? ifritGates(task, s.gates).join(" ") : ""),
    "",
  );
  const gateWait = useOptionalData((s) => {
    const wait = ifritWaitOf(task, s.gates);
    if (!wait) return "";
    return wait.release
      ? t("ifrit.gate_wait", {
          gate: wait.gate,
          holder: wait.holder,
          time: wait.release,
        })
      : t("ifrit.gate_wait_held", { gate: wait.gate, holder: wait.holder });
  }, "");
  const markable = !!onDone && closable(task) && !ifrit;
  const urgeable = !!onUrge && canUrge(task);
  const urgent = isUrgent(task);
  const hasActions = urgeable || holdable || stoppable || markable;
  const machineWait = task.status === TaskStatus.PENDING && task.machineWait;
  const waitReason = liveWaitReason(task);
  return (
    <article
      className={`wish-task tone-${tone} ${open ? "open" : ""} ${focused ? "focused" : ""}`}
      id={`task-${task.id}`}
    >
      <div className={`wish-task-row ${hasActions ? "has-actions" : ""}`}>
        <button
          className="wish-task-heading"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          <span className="wish-task-first-line">
            <StatusBadge
              tone={tone}
              label={
                lit || out
                  ? ifritWord(task, now)
                  : taskStatusText(task, forkedAs, fixedBy)
              }
              title={cancelledText(task) ?? activity ?? undefined}
            />
            {isMerge(task) && <MergeLabel task={task} codes={codes} />}
            {isUrgent(task) && <UrgentLabel feedback={task.feedback} />}
            {isInvestigation(task) && <InvestigationLabel />}
            {isMockup(task) && <MockupLabel />}
            <span className="agent-code">{task.code}</span>
            <HealthMark task={task} />
            {/* Cut to the room left: the whole title shows on hover. */}
            <span className="task-title-wrap">
              <strong className="task-title">{task.title}</strong>
              <span className="task-tooltip" role="tooltip">
                <span className="task-tooltip-title">{task.title}</span>
                {activity ? (
                  <span className="task-tooltip-activity">{activity}</span>
                ) : null}
                {preview ? (
                  <span className="task-tooltip-prompt">{preview}</span>
                ) : null}
              </span>
            </span>
            <ChevronDown size={14} className={open ? "rotated" : ""} />
          </span>
          <span className="wish-task-second-line">
            <span className="wish-task-meta">
              {project?.name && (
                <span className="task-project">{project.name}</span>
              )}
              {(() => {
                const display = taskModelDisplay(task, project);
                return display ? (
                  <span className="task-model" title={display.title}>
                    {display.label}
                  </span>
                ) : null;
              })()}
              {time.text && (
                <span className="task-time" title={time.title}>
                  {time.text}
                </span>
              )}
              <TaskTurns task={task} />
              {origin}
              <TaskUsage task={task} />
              {task.status === TaskStatus.RUNNING &&
                task.resources?.readTime && (
                  <span
                    className="task-usage"
                    title={resourcesDetail(task.resources, true)}
                  >
                    {resourcesNow(task.resources)}
                  </span>
                )}
            </span>
          </span>
        </button>
        {hasActions && (
          <span className="wish-task-actions">
            {urgeable && (
              <button
                className={`icon-button ${urgent ? "active" : ""}`}
                onClick={() => onUrge?.(task, !urgent)}
                title={t(urgent ? "task.unurge_detail" : "task.urge_detail")}
                aria-label={t("task.urge")}
                aria-pressed={urgent}
              >
                <Zap size={14} />
              </button>
            )}
            {holdable && (
              <button
                className="icon-button"
                onClick={() => onHold?.(!paused, task)}
                title={t(paused ? "task.resume_detail" : "task.pause_detail")}
                aria-label={t(paused ? "task.resume" : "task.pause")}
              >
                {paused ? <CirclePlay size={14} /> : <CirclePause size={14} />}
              </button>
            )}
            {stoppable && (
              <button
                className="icon-button"
                onClick={() => onStop(task)}
                title={t("task.stop")}
                aria-label={t("task.stop")}
              >
                <CircleStop size={14} />
              </button>
            )}
            {markable && (
              <button
                className="icon-button"
                onClick={() => setClosing(!closing)}
                title={t("task.mark_done_detail")}
                aria-label={t("task.mark_done")}
                aria-expanded={closing}
              >
                <CheckCircle2 size={14} />
              </button>
            )}
          </span>
        )}
      </div>
      {decision && onDecision && (
        <p className="wish-task-note wish-task-decision">
          <DecisionLink decision={decision} onOpen={() => onDecision(task)} />
        </p>
      )}
      {closing && onDone && markable && (
        <DoneBox
          onDone={(note) => onDone(note, task)}
          onCancel={() => setClosing(false)}
        />
      )}
      {(lit || out) && (
        <div className="wish-task-note ifrit-line">
          <IfritLinks links={ifritLinks(task)} />
          <IfritGates gates={held ? held.split(" ") : []} />
          {phase === "warned" && <IfritCountdown task={task} now={now} />}
          <span className="ifrit-actions">
            {phase === "warned" && <KeepLit />}
            {out && onRelight && (
              <button
                type="button"
                className="button secondary small"
                onClick={() => setRelighting(!relighting)}
                title={t("ifrit.relight_detail")}
                aria-expanded={relighting}
              >
                <Flame size={13} aria-hidden="true" />
                <span>{t("ifrit.relight")}</span>
              </button>
            )}
            {onDone && (
              <button
                type="button"
                className="button secondary small"
                onClick={() => void onDone("", task).catch(() => {})}
                title={t("ifrit.finish_detail")}
              >
                <CheckCircle2 size={13} aria-hidden="true" />
                <span>{t("ifrit.finish")}</span>
              </button>
            )}
          </span>
        </div>
      )}
      {relighting && out && onRelight && (
        <SendBox
          label={t("ifrit.relight_placeholder")}
          onSend={(text) =>
            onRelight(text, task).then(() => setRelighting(false))
          }
        />
      )}
      {reply && (
        <p className="wish-task-note ifrit-reply" title={t("ifrit.reply")}>
          <q>{reply}</q>
        </p>
      )}
      {gateWait && (
        <p className="wish-task-note ifrit-wait tone-ifrit">
          <ToneIcon tone="ifrit" size={12} />
          <span>{gateWait}</span>
        </p>
      )}
      {task.closed && (
        <p className="wish-task-note wish-task-closed">
          {t(
            task.closed.actor === Closer.DEVELOPER
              ? "task.closed_by_you"
              : "task.closed_by_lead",
            { when: when(task.closed.createTime) },
          )}
          {task.closed.continuedIn ? (
            <>
              {": "}
              {t("task.continued_in")}{" "}
              <TaskLink code={task.closed.continuedIn} codes={codes} />
            </>
          ) : (
            task.closed.note && `: ${task.closed.note}`
          )}
        </p>
      )}
      {work && (
        <p className={`wish-task-note wish-task-work work-${work.tone}`}>
          {work.text}
          {work.fixedBy && (
            <>
              {`, ${t("work.fixing_by")} `}
              <TaskLink code={work.fixedBy} codes={codes} />
            </>
          )}
        </p>
      )}
      {machineWait ? (
        <div className="wish-task-note machine-wait">
          <strong>{t("task.machine_wait")}</strong>
          <span>{waitReason}</span>
          {onStart && (
            <button
              className="button secondary small"
              onClick={() => onStart(task)}
              title={t("task.start_anyway_detail")}
            >
              <Play size={13} />
              <span>{t("task.start_anyway")}</span>
            </button>
          )}
        </div>
      ) : (
        (waitReason ||
          task.error ||
          after.length > 0 ||
          (watcher && task.lastLine)) && (
          <p className={`wish-task-note ${tone === "failed" ? "error" : ""}`}>
            {task.error ? (
              task.error
            ) : isMemoryWait(waitReason) ? (
              <span title={waitReason}>{t("task.waits_for_memory")}</span>
            ) : isGreenWait(waitReason) ? (
              <span title={waitReason}>{compactGreenWait(waitReason)}</span>
            ) : waitReason ? (
              <WaitReason reason={waitReason} codes={codes} />
            ) : (
              watcher && task.lastLine
            )}
            {after.length > 0 && (
              <span className="wish-task-after">
                {t("page.after", { tasks: after.join(", ") })}
              </span>
            )}
          </p>
        )
      )}
      {alsoMerged?.map(({ merge, host }) => (
        <p className="wish-task-note wish-task-merged-in" key={merge.id}>
          <button
            type="button"
            className="text-button"
            onClick={() => jump(`task-${host.id}`)}
            title={t("task.merge_also", { task: merge.code, host: host.code })}
          >
            <span className="merge-label">{t("task.merge")}</span>
            <span className="agent-code">{merge.code}</span>
          </button>
        </p>
      ))}
      {merges && merges.length > 0 && (
        <MergeFold merges={merges} opened={mergeOpen}>
          {nested}
        </MergeFold>
      )}
      {open && (
        <TaskBody
          task={task}
          forkOf={codes?.get(task.forkOf ?? "") ?? ""}
          prompt={prompt}
          onTurnComments={onTurnComments}
        />
      )}
      {open && task.status === TaskStatus.RUNNING && !watcher && (
        <SendBox onSend={(text) => onSend(text, task)} />
      )}
    </article>
  );
});

// MergeLabel marks a merge, compact; what it brings in on hover.
export function MergeLabel({
  task,
  codes,
}: {
  task: Task;
  codes?: ReadonlyMap<string, string>;
}) {
  const served = (
    task.correction?.failure?.taskIds ??
    task.review?.taskIds ??
    []
  )
    .map((id) => codes?.get(id))
    .filter((code): code is string => !!code);
  return (
    <span
      className="merge-label"
      title={
        served.length
          ? t("task.merge_detail", { tasks: served.join(", ") })
          : undefined
      }
    >
      {t("task.merge")}
    </span>
  );
}

// HealthMark marks a worker the scheduler flagged to relaunch (Task.health, W188): stalled, or failed for a reason a
// new turn may get past. Its tooltip says why, and the command that relaunches it. Nothing for any other task.
export function HealthMark({ task }: { task: Task }) {
  const state = task.health?.state;
  if (state !== HealthState.STALLED && state !== HealthState.RETRIABLE) {
    return null;
  }
  const stalled = state === HealthState.STALLED;
  const label = t(stalled ? "task.health_stalled" : "task.health_retriable");
  const title = t("task.health_detail", {
    what: label,
    reason: task.health?.reason ?? "",
    command: `djinn task continue ${task.id}`,
  });
  return (
    <span
      className={`health-mark ${stalled ? "stalled" : "retriable"}`}
      role="img"
      aria-label={label}
      title={title}
    >
      <RotateCcw size={12} />
    </span>
  );
}

// UrgentLabel marks a task the lead made urgent (isUrgent): to unblock the wish, or for the developer's quick feedback
// (a mockup, a change they wait to see). Either passes the queue, a repair first.
export function UrgentLabel({ feedback }: { feedback: boolean }) {
  return (
    <span
      className="urgent-label"
      title={t(feedback ? "task.feedback_detail" : "task.urgent_detail")}
    >
      {t(feedback ? "task.feedback" : "task.urgent")}
    </span>
  );
}

// InvestigationLabel marks a study or audit the lead launched to align with the developer (isInvestigation): served
// first, it ends in a tilasm and a question, and its commits are never merged.
export function InvestigationLabel() {
  return (
    <span
      className="investigation-label"
      title={t("task.investigation_detail")}
    >
      {t("task.investigation")}
    </span>
  );
}

// MockupLabel marks a mockup task in the list, the tree and the glance: a design worker in a tilasm, never merged.
export function MockupLabel() {
  return (
    <span className="mockup-label" title={t("task.mockup_detail")}>
      {t("task.mockup")}
    </span>
  );
}

// MergeFold folds the merges of a task under it, the label, their codes and the latest one's state on its line; the
// rows open on a click, or when one of them is brought into sight.
function MergeFold({
  merges,
  opened,
  children,
}: {
  merges: readonly Task[];
  opened: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(opened);
  useEffect(() => {
    if (opened) setOpen(true);
  }, [opened]);
  const latest = merges[merges.length - 1];
  return (
    <div className={`wish-task-merges ${open ? "open" : ""}`}>
      <button
        type="button"
        className="wish-task-merges-toggle"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        title={merges.map((m) => `${m.code} ${m.title}`).join("\n")}
      >
        <ChevronRight size={12} className={open ? "rotated-90" : ""} />
        <span className="merge-label">{t("task.merge")}</span>
        <span className="agent-code">
          {merges.map((m) => m.code).join(", ")}
        </span>
        <StatusBadge
          tone={taskTone(latest, "")}
          label={taskStatusText(latest, "")}
        />
      </button>
      {open && <div className="wish-task-merge-list">{children}</div>}
    </div>
  );
}

type Work = {
  text: string;
  tone: "waiting" | "running" | "done" | "failed" | "fixing";
  // The correction task that fixes the failed merge while it works, by its code: the page links to it after the text.
  fixedBy?: string;
};

// taskWork says where the finished work of a task stands on its way into its wish's integration branch (T07), as the
// page says it, and its tone: undefined for work Djinn does not integrate. codes name the correction worker's task;
// fixedBy is that task's code while it fixes a failed merge (fixerOf): the failure is then being fixed, not red.
export function taskWork(
  task: Task,
  codes?: ReadonlyMap<string, string>,
  fixedBy = "",
): Work | undefined {
  const work = task.integration;
  const sha = (work?.sha ?? "").slice(0, 8);
  let out: Work;
  switch (work?.state) {
    case IntegrationState.PENDING:
      out = {
        text: work.reason
          ? t("work.pending_why", { reason: gitReason(work.reason) })
          : t("work.pending"),
        tone: "waiting",
      };
      break;
    case IntegrationState.INTEGRATING:
      out = {
        text: t("work.integrating", { branch: work.branch }),
        tone: "running",
      };
      break;
    case IntegrationState.COMMITTED:
      if (!sha && !work.correctedBy && !work.reviewedBy) return undefined;
      out = {
        text: t("work.committed", { branch: work.branch, sha }),
        tone: "done",
      };
      break;
    case IntegrationState.CONFLICT:
      out = {
        text: t("work.conflict", { reason: work.reason }),
        tone: "failed",
      };
      break;
    case IntegrationState.RED:
      out = { text: t("work.red", { reason: work.reason }), tone: "failed" };
      break;
    case IntegrationState.UNCOMMITTED:
      out = { text: t("work.uncommitted"), tone: "waiting" };
      break;
    default:
      return undefined;
  }
  if (out.tone === "failed" && fixedBy)
    out = { ...out, tone: "fixing", fixedBy };
  else if (work.correctedBy)
    out.text += `, ${t("work.corrected_by", { task: codes?.get(work.correctedBy) ?? work.correctedBy })}`;
  if (work.reviewedBy)
    out.text += `, ${t("work.reviewed_by", { task: codes?.get(work.reviewedBy) ?? work.reviewedBy })}`;
  return out;
}

// TaskLink is a task of the wish by its code: a click shows its card. A code no card holds stays text.
function TaskLink({
  code,
  codes,
}: {
  code: string;
  codes?: ReadonlyMap<string, string>;
}) {
  const id = [...(codes ?? [])].find(([, c]) => c === code)?.[0];
  if (!id) return <span className="agent-code">{code}</span>;
  return (
    <button
      type="button"
      className="text-button agent-code task-link"
      onClick={() => jump(`task-${id}`)}
    >
      {code}
    </button>
  );
}

// closable tells a task a person may mark done by hand: one no worker runs now, not done or cancelled already.
export function closable(task: Task): boolean {
  const status = task.status;
  return (
    status !== TaskStatus.RUNNING &&
    status !== TaskStatus.PAUSED &&
    status !== TaskStatus.DONE &&
    status !== TaskStatus.CANCELLED
  );
}

// DoneBox marks the task done, with an optional note; djinn's refusal shows as a toast, and the box stays.
function DoneBox({
  onDone,
  onCancel,
}: {
  onDone: (note: string) => Promise<unknown>;
  onCancel: () => void;
}) {
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await onDone(note.trim());
    } catch {
      // The toast says why.
      setBusy(false);
    }
  };
  return (
    <form className="wish-task-send wish-task-done" onSubmit={submit}>
      <input
        value={note}
        onChange={(event) => setNote(event.target.value)}
        placeholder={t("task.done_note")}
        aria-label={t("task.done_note")}
        disabled={busy}
        autoFocus
      />
      <button
        className="button secondary small"
        type="button"
        onClick={onCancel}
      >
        {t("common.cancel")}
      </button>
      <button className="button accent small" type="submit" disabled={busy}>
        <CheckCircle2 size={14} />
        {t("task.mark_done")}
      </button>
    </form>
  );
}

function TaskBody({
  task,
  forkOf,
  prompt,
  onTurnComments,
}: {
  task: Task;
  forkOf: string;
  prompt: string;
  onTurnComments?: (task: Task) => Promise<unknown>;
}) {
  const [showLogs, setShowLogs] = useState(false);
  // The review of the task's diffs, full size over the main shell, on its committed work or on its worktree; null
  // while closed. The button that opened it takes the focus back once it closes.
  const [diff, setDiff] = useState<{
    view: DiffView;
    shell: HTMLElement;
  } | null>(null);
  const opener = useRef<HTMLElement | null>(null);
  const openDiff = (view: DiffView, from: HTMLElement) => {
    opener.current = from;
    setDiff({
      view,
      shell: from.closest<HTMLElement>(".main-shell") ?? document.body,
    });
  };
  // Once the review is gone and what it covered given back (its effects end before this one runs).
  useEffect(() => {
    if (diff) return;
    opener.current?.focus({ preventScroll: true });
    opener.current = null;
  }, [diff]);
  const events = useTaskEvents(task.id, task.status, showLogs);
  // A wait a start, a resume or another wait ended is past: folded, muted, into the line that ended it.
  const folded = useMemo(() => foldedWaits(events), [events]);
  const past = useMemo(
    () => new Set([...folded.values()].flat().map((e) => e.id)),
    [folded],
  );
  const box = useRef<HTMLDivElement>(null);
  // Keep the newest event in sight while the worker runs, inside the box: the page stays where it is.
  useEffect(() => {
    const element = box.current;
    if (element && showLogs && !taskFinished(task.status))
      element.scrollTop = element.scrollHeight;
  }, [events.length, task.status, showLogs]);
  // A finished worker's last text is its report: it shows first, as Markdown.
  const lastWord = taskFinished(task.status) ? lastText(events) : "";
  const spent = usageDetail(task.usage, task.model, task.provider);
  const running = task.status === TaskStatus.RUNNING;
  const used = task.resources ? resourcesDetail(task.resources, running) : "";
  const djinn = useDjinn();
  // Where Djinn does not measure workers (Windows), a running worker says so.
  const unmeasured =
    useOptionalData((s) => !!s.machine?.workerMeasure, false) && running;
  const scopes = task.writeScopes ?? [];
  return (
    <div className="wish-task-body">
      <div className="wish-task-facts">
        <span>{agentOf(task)}</span>
        {task.branch && <span>{task.branch}</span>}
        {task.startTime && (
          <span>{t("task.started", { when: when(task.startTime) })}</span>
        )}
        {task.endTime && (
          <span>{t("task.ended", { when: when(task.endTime) })}</span>
        )}
        {scopes.length > 0 && (
          <span>{t("task.scopes", { scopes: scopes.join(", ") })}</span>
        )}
        {forkOf && <span>{t("task.fork_of", { task: forkOf })}</span>}
        {task.maxBudgetUsd > 0 && (
          <span>{t("task.budget", { cost: usd(task.maxBudgetUsd) })}</span>
        )}
        {spent && (
          <span title={costTitle(task.usage, task.model, task.provider)}>
            {spent}
          </span>
        )}
        {used && <span>{used}</span>}
        {/* A running worker's row in the Hourglass: its CPU and memory over time. */}
        {used && running && djinn && (
          <button
            type="button"
            className="text-link task-hourglass-open"
            onClick={() =>
              djinn.focus.show({ page: HOURGLASS, target: `worker-${task.id}` })
            }
          >
            {t("hourglass.worker")}
          </button>
        )}
        {!used && unmeasured && <span>{t("resources.not_measured")}</span>}
      </div>
      <TaskPromptFold prompt={prompt} />
      {lastWord && (
        <div className="wish-task-last-word">
          <span className="eyebrow">{t("page.last_word")}</span>
          <MarkdownBody text={lastWord} />
        </div>
      )}
      <details className="wish-task-events-fold" open={showLogs}>
        <summary
          className="button secondary small wish-task-logs-button"
          role="button"
          onClick={(e) => {
            e.preventDefault();
            setShowLogs(!showLogs);
          }}
        >
          {showLogs ? t("task.hide_logs") : t("task.see_logs")}
        </summary>
        {showLogs && (
          <div className="wish-task-events" ref={box} aria-live="polite">
            {events.length === 0 ? (
              <p className="muted-text">{t("task.no_events")}</p>
            ) : (
              <table className="compact-table event-table">
                <tbody>
                  {events
                    .filter((event) => !past.has(event.id))
                    .map((event) => (
                      <EventLine
                        key={event.id}
                        event={event}
                        waits={folded.get(event.id)}
                        model={task.model}
                        provider={task.provider}
                      />
                    ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </details>
      {onTurnComments && commentsWaiting(task) && (
        <TurnCommentsButton task={task} onTurn={onTurnComments} />
      )}
      <div className="wish-task-diff-actions">
        <button
          type="button"
          className="button secondary small wish-task-diff-button"
          onClick={(e) => openDiff("committed", e.currentTarget)}
        >
          {t("task.see_diff")}
        </button>
        <button
          type="button"
          className="button secondary small wish-task-worktree-button"
          disabled={!task.worktree}
          title={task.worktree ? undefined : t("task.no_worktree")}
          onClick={(e) => openDiff("uncommitted", e.currentTarget)}
        >
          {t("task.see_worktree")}
        </button>
      </div>
      {diff && (
        <TaskReview
          task={task}
          view={diff.view}
          shell={diff.shell}
          onView={(view) => setDiff({ ...diff, view })}
          onClose={(back) => {
            if (!back) opener.current = null;
            setDiff(null);
          }}
        />
      )}
    </div>
  );
}

// commentsWaiting tells whether an ended task has comments on its diff that went to no worker and are not resolved:
// TaskService.TurnComments takes them. A cancelled task's work is dropped, comments with it.
function commentsWaiting(task: Task): boolean {
  const ended =
    task.status === TaskStatus.DONE ||
    task.status === TaskStatus.FAILED ||
    task.status === TaskStatus.STOPPED ||
    task.status === TaskStatus.INTERRUPTED;
  return ended && (task.comments ?? []).some((c) => !c.resolved && !c.sentTo);
}

export function TurnCommentsButton({
  task,
  onTurn,
}: {
  task: Task;
  onTurn: (task: Task) => Promise<unknown>;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      className="button secondary small wish-task-turn-comments-button"
      disabled={busy}
      onClick={() => {
        setBusy(true);
        onTurn(task).finally(() => setBusy(false));
      }}
    >
      {t("task.turn_comments")}
    </button>
  );
}

// lastText is the last text a worker wrote.
function lastText(events: readonly TaskEvent[]): string {
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (e.kind === TaskEventKind.TEXT && e.text.trim()) return e.text;
  }
  return "";
}

// clock is an event's hour and minute.
function clock(event: TaskEvent): string {
  if (!event.createTime) return "";
  return new Date(Number(event.createTime.seconds) * 1000).toLocaleTimeString(
    language,
    {
      hour: "2-digit",
      minute: "2-digit",
    },
  );
}

// SendBox sends the running worker an instruction, or the message that lights an ifrit again (label); djinn's refusal
// shows as a toast, and the text stays.
function SendBox({
  onSend,
  label = t("task.message_placeholder"),
}: {
  onSend: (text: string) => Promise<unknown>;
  label?: string;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const message = text.trim();
    if (!message || busy) return;
    setBusy(true);
    try {
      await onSend(message);
      setText("");
    } catch {
      // The toast says why.
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="wish-task-send" onSubmit={submit}>
      <input
        value={text}
        onChange={(event) => setText(event.target.value)}
        placeholder={label}
        aria-label={label}
        disabled={busy}
      />
      <button
        className="icon-button"
        type="submit"
        disabled={busy || !text.trim()}
        title={t("task.message_send")}
        aria-label={t("task.message_send")}
      >
        <Send size={14} />
      </button>
    </form>
  );
}

// An event never changes once kept: each push of new events draws only the new rows, and the line that ends a wait,
// which takes in the "waiting:" lines it ended (waits), muted, above its own text.
const EventLine = memo(function EventLine({
  event,
  waits,
  model,
  provider,
}: {
  event: TaskEvent;
  waits?: TaskEvent[];
  model?: string;
  provider?: Provider;
}) {
  const Icon = eventIcons[event.kind] ?? FileText;
  const usage = event.kind === TaskEventKind.USAGE ? event.usage : undefined;
  const text = usage
    ? usageDetail(usage, model, provider)
    : event.kind === TaskEventKind.RECEIVED
      ? t("task.received", { text: event.text })
      : event.text;
  return (
    <tr className={`wish-event kind-${event.kind}`}>
      <td>
        <time>{clock(event)}</time>
      </td>
      <td title={TaskEventKind[event.kind]?.toLowerCase()}>
        <Icon size={13} aria-hidden="true" />
      </td>
      <td
        className="wish-event-text"
        title={usage ? costTitle(usage, model, provider) : undefined}
      >
        {waits?.map((wait) => (
          <span key={wait.id} className="wish-event-waited">
            <time>{clock(wait)}</time> {wait.text}
          </span>
        ))}
        {text}
      </td>
    </tr>
  );
});

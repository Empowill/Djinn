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
  CirclePause,
  CirclePlay,
  CircleStop,
  FileText,
  Gauge,
  Lock,
  MessageSquare,
  MessageSquarePlus,
  ScrollText,
  Send,
  Terminal,
} from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  Closer,
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
import { useData, useTaskEvents } from "./data/djinn";
import {
  shortModel,
  taskFinished,
  taskStatusText,
  taskTime,
  taskTone,
  usd,
  useNow,
  when,
} from "./data/format";
import { DecisionLink } from "./decision-log";
import { language, t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";
import { resourcesDetail, resourcesNow, TaskUsage, usageDetail } from "./usage";

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

export function WishTask({
  task,
  project,
  codes,
  origin,
  forkedAs = "",
  decision,
  focused = false,
  onStop,
  onSend,
  onHold,
  onDecision,
  onDone,
}: {
  task: Task;
  project?: Project;
  // The codes of the wish's tasks, by id: what this one waits for, what it was forked from.
  codes?: ReadonlyMap<string, string>;
  // Where the task comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  // The task that took over this one, cut short: its code.
  forkedAs?: string;
  // The decision the task comes from, which onDecision shows.
  decision?: Decision;
  // Brought into sight and opened, from its decision.
  focused?: boolean;
  onStop: () => void;
  onSend: (text: string) => Promise<unknown>;
  // Pauses the task's worker (true) or lets it go on (false); none where djinn cannot pause one (Windows).
  onHold?: (pause: boolean) => void;
  onDecision?: () => void;
  // Marks the task done by hand, with a note; none where the task cannot be closed from here.
  onDone?: (note: string) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(focused);
  const [closing, setClosing] = useState(false);
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
  const tone = taskTone(task, forkedAs);
  // How long it runs comes first: ticking while it runs, how long it ran once ended.
  const now = useNow(!taskFinished(task.status) && !!task.startTime);
  const time = taskTime(task, now);
  const work = taskWork(task, codes);
  return (
    <article
      className={`wish-task tone-${tone} ${open ? "open" : ""} ${focused ? "focused" : ""}`}
      id={`task-${task.id}`}
    >
      <div className="wish-task-row">
        <button
          className="wish-task-heading"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          <StatusBadge tone={tone} label={taskStatusText(task, forkedAs)} />
          <span className="agent-code">{task.code}</span>
          {project?.name && (
            <span className="task-project">{project.name}</span>
          )}
          {/* Cut to the room left: the whole title shows on hover. */}
          <strong title={task.title}>{task.title}</strong>
          <span className="wish-task-meta">
            {origin}
            {time.text && (
              <span className="task-time" title={time.title}>
                {time.text}
              </span>
            )}
            {!watcher && task.model && (
              <span className="task-model" title={task.model}>
                {shortModel(task.model)}
              </span>
            )}
            <TaskUsage usage={task.usage} />
            {task.status === TaskStatus.RUNNING && task.resources?.readTime && (
              <span
                className="task-usage"
                title={resourcesDetail(task.resources, true)}
              >
                {resourcesNow(task.resources)}
              </span>
            )}
          </span>
          <ChevronDown size={14} className={open ? "rotated" : ""} />
        </button>
        {holdable && (
          <button
            className="icon-button"
            onClick={() => onHold?.(!paused)}
            title={t(paused ? "task.resume_detail" : "task.pause_detail")}
            aria-label={t(paused ? "task.resume" : "task.pause")}
          >
            {paused ? <CirclePlay size={14} /> : <CirclePause size={14} />}
          </button>
        )}
        {stoppable && (
          <button
            className="icon-button"
            onClick={onStop}
            title={t("task.stop")}
            aria-label={t("task.stop")}
          >
            <CircleStop size={14} />
          </button>
        )}
        {onDone && closable(task.status) && (
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
      </div>
      {decision && onDecision && (
        <p className="wish-task-note wish-task-decision">
          <DecisionLink decision={decision} onOpen={onDecision} />
        </p>
      )}
      {closing && onDone && closable(task.status) && (
        <DoneBox onDone={onDone} onCancel={() => setClosing(false)} />
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
        </p>
      )}
      {(task.waitReason ||
        task.error ||
        after.length > 0 ||
        (watcher && task.lastLine)) && (
        <p className={`wish-task-note ${tone === "failed" ? "error" : ""}`}>
          {task.error || task.waitReason || (watcher && task.lastLine)}
          {after.length > 0 && (
            <span className="wish-task-after">
              {t("page.after", { tasks: after.join(", ") })}
            </span>
          )}
        </p>
      )}
      {open && (
        <TaskBody task={task} forkOf={codes?.get(task.forkOf ?? "") ?? ""} />
      )}
      {open && task.status === TaskStatus.RUNNING && !watcher && (
        <SendBox onSend={onSend} />
      )}
    </article>
  );
}

// taskWork says where the finished work of a task stands on its way into its wish's integration branch (T07), as the
// page says it, and its tone: undefined for work Djinn does not integrate. codes name the correction worker's task.
export function taskWork(
  task: Task,
  codes?: ReadonlyMap<string, string>,
):
  | { text: string; tone: "waiting" | "running" | "done" | "failed" }
  | undefined {
  const work = task.integration;
  const sha = (work?.sha ?? "").slice(0, 8);
  let out: { text: string; tone: "waiting" | "running" | "done" | "failed" };
  switch (work?.state) {
    case IntegrationState.PENDING:
      out = {
        text: work.reason
          ? t("work.pending_why", { reason: work.reason })
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
  if (work.correctedBy)
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

// closable tells a task a person may mark done by hand: one no worker runs now, not done already.
export function closable(status: TaskStatus): boolean {
  return (
    status !== TaskStatus.RUNNING &&
    status !== TaskStatus.PAUSED &&
    status !== TaskStatus.DONE
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

function TaskBody({ task, forkOf }: { task: Task; forkOf: string }) {
  const events = useTaskEvents(task.id, task.status);
  const box = useRef<HTMLDivElement>(null);
  // Keep the newest event in sight while the worker runs, inside the box: the page stays where it is.
  useEffect(() => {
    const element = box.current;
    if (element && !taskFinished(task.status))
      element.scrollTop = element.scrollHeight;
  }, [events.length, task.status]);
  // A finished worker's last text is its report: it shows first, as Markdown.
  const lastWord = taskFinished(task.status) ? lastText(events) : "";
  const spent = usageDetail(task.usage);
  const running = task.status === TaskStatus.RUNNING;
  const used = task.resources ? resourcesDetail(task.resources, running) : "";
  // Where Djinn does not measure workers (Windows), a running worker says so.
  const unmeasured = useData((s) => !!s.machine?.workerMeasure) && running;
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
        {spent && <span>{spent}</span>}
        {used && <span>{used}</span>}
        {!used && unmeasured && <span>{t("resources.not_measured")}</span>}
      </div>
      {lastWord && (
        <div className="wish-task-last-word">
          <span className="eyebrow">{t("page.last_word")}</span>
          <MarkdownBody text={lastWord} />
        </div>
      )}
      <details
        className="wish-task-events-fold"
        open={!taskFinished(task.status)}
      >
        <summary>{t("task.events", { count: events.length })}</summary>
        <div className="wish-task-events" ref={box} aria-live="polite">
          {events.length === 0 ? (
            <p className="muted-text">{t("task.no_events")}</p>
          ) : (
            <table className="compact-table event-table">
              <tbody>
                {events.map((event) => (
                  <EventLine key={event.id} event={event} />
                ))}
              </tbody>
            </table>
          )}
        </div>
      </details>
    </div>
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

// SendBox sends the running worker an instruction; djinn's refusal shows as a toast, and the text stays.
function SendBox({ onSend }: { onSend: (text: string) => Promise<unknown> }) {
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
        placeholder={t("task.message_placeholder")}
        aria-label={t("task.message_placeholder")}
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

function EventLine({ event }: { event: TaskEvent }) {
  const Icon = eventIcons[event.kind] ?? FileText;
  const text =
    event.kind === TaskEventKind.USAGE && event.usage
      ? usageDetail(event.usage)
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
      <td className="wish-event-text">{text}</td>
    </tr>
  );
}

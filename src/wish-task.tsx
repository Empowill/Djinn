// A task of a wish: its code, what it does, where it stands, what it spent, and, opened, its facts, its worker's
// last word and its events as they come (TaskService.Watch), with a box to send the running worker an instruction
// (TaskService.Send).
import {
  Activity,
  AlertCircle,
  CheckCheck,
  ChevronDown,
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
  type Project,
  Provider,
  type Task,
  type TaskEvent,
  TaskEventKind,
  TaskStatus,
} from "../gen/ts/plan/v1/plan_pb";
import { useTaskEvents } from "./data/djinn";
import {
  taskFinished,
  taskStatusText,
  taskTone,
  usd,
  when,
} from "./data/format";
import { language, t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { TaskUsage, usageDetail } from "./usage";

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
  onStop,
  onSend,
}: {
  task: Task;
  project?: Project;
  // The codes of the wish's tasks, by id: what this one waits for, what it was forked from.
  codes?: ReadonlyMap<string, string>;
  // Where the task comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  onStop: () => void;
  onSend: (text: string) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const stoppable =
    task.status === TaskStatus.RUNNING ||
    task.status === TaskStatus.PAUSED ||
    task.status === TaskStatus.PENDING;
  const after = taskFinished(task.status)
    ? []
    : (task.dependsOn ?? [])
        .map((id) => codes?.get(id))
        .filter((code): code is string => !!code);
  return (
    <article className={`wish-task ${open ? "open" : ""}`}>
      <div className="wish-task-row">
        <button
          className="wish-task-heading"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          <span className={`mission-dot ${taskTone(task.status)}`} />
          <span className="agent-code">{task.code}</span>
          <strong>{task.title}</strong>
          <span className="wish-task-meta">
            {origin}
            {project?.name && <span>{project.name}</span>}
            <span>{taskStatusText(task.status)}</span>
            <TaskUsage usage={task.usage} />
          </span>
          <ChevronDown size={14} className={open ? "rotated" : ""} />
        </button>
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
      </div>
      {(task.waitReason || task.error || after.length > 0) && (
        <p
          className={`wish-task-note ${taskTone(task.status) === "error" ? "error" : ""}`}
        >
          {task.error || task.waitReason}
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
      {open && task.status === TaskStatus.RUNNING && (
        <SendBox onSend={onSend} />
      )}
    </article>
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
      </div>
      {lastWord && (
        <div className="wish-task-last-word">
          <span className="eyebrow">{t("page.last_word")}</span>
          <MarkdownBody text={lastWord} />
        </div>
      )}
      <div className="wish-task-events" ref={box} aria-live="polite">
        {events.length === 0 && (
          <p className="muted-text">{t("task.no_events")}</p>
        )}
        {events.map((event) => (
          <EventLine key={event.id} event={event} />
        ))}
      </div>
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
    <div className={`wish-event kind-${event.kind}`}>
      <time>{clock(event)}</time>
      <Icon size={13} />
      <span className="wish-event-text">{text}</span>
    </div>
  );
}

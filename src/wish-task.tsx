// A task of a wish: its code, what it does, where it stands, and, opened, its worker's events as they come
// (TaskService.Watch), with a box to send the running worker an instruction (TaskService.Send).
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
  Send,
  Terminal,
} from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";

import {
  type Project,
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
import { t } from "./i18n";

const eventIcons: Partial<Record<TaskEventKind, typeof FileText>> = {
  [TaskEventKind.PROMPT]: MessageSquare,
  [TaskEventKind.TEXT]: FileText,
  [TaskEventKind.TOOL_CALL]: Terminal,
  [TaskEventKind.TOOL_RESULT]: Terminal,
  [TaskEventKind.USAGE]: Gauge,
  [TaskEventKind.STATUS]: Activity,
  [TaskEventKind.ERROR]: AlertCircle,
  [TaskEventKind.GATE]: Lock,
  [TaskEventKind.MESSAGE]: MessageSquarePlus,
  [TaskEventKind.RECEIVED]: CheckCheck,
};

export function WishTask({
  task,
  project,
  onStop,
  onSend,
}: {
  task: Task;
  project?: Project;
  onStop: () => void;
  onSend: (text: string) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const stoppable =
    task.status === TaskStatus.RUNNING || task.status === TaskStatus.PENDING;
  const cost = task.usage?.costUsd ?? 0;
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
            {project?.name && <span>{project.name}</span>}
            <span>{taskStatusText(task.status)}</span>
            {cost > 0 && <span>{usd(cost)}</span>}
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
      {(task.waitReason || task.error) && (
        <p
          className={`wish-task-note ${taskTone(task.status) === "error" ? "error" : ""}`}
        >
          {task.error || task.waitReason}
        </p>
      )}
      {open && <TaskEvents task={task} />}
      {open && task.status === TaskStatus.RUNNING && (
        <SendBox onSend={onSend} />
      )}
    </article>
  );
}

function TaskEvents({ task }: { task: Task }) {
  const events = useTaskEvents(task.id, task.status);
  const box = useRef<HTMLDivElement>(null);
  // Keep the newest event in sight while the worker runs, inside the box: the page stays where it is.
  useEffect(() => {
    const element = box.current;
    if (element && !taskFinished(task.status))
      element.scrollTop = element.scrollHeight;
  }, [events.length, task.status]);
  return (
    <div className="wish-task-events" ref={box} aria-live="polite">
      <div className="wish-task-facts">
        {task.branch && <span>{task.branch}</span>}
        {task.model && <span>{task.model}</span>}
        {task.startTime && (
          <span>{t("task.started", { when: when(task.startTime) })}</span>
        )}
        {task.endTime && (
          <span>{t("task.ended", { when: when(task.endTime) })}</span>
        )}
      </div>
      {events.length === 0 && (
        <p className="muted-text">{t("task.no_events")}</p>
      )}
      {events.map((event) => (
        <EventLine key={event.id} event={event} />
      ))}
    </div>
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
      ? usd(event.usage.costUsd)
      : event.kind === TaskEventKind.RECEIVED
        ? t("task.received", { text: event.text })
        : event.text;
  return (
    <div className={`wish-event kind-${event.kind}`}>
      <Icon size={13} />
      <span className="wish-event-text">{text}</span>
    </div>
  );
}

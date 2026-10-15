// The decision log, a tab of its own in a wish's view and in the flight plan: every decision taken, the latest first,
// read only. No button: an answered question was answered, a decision block was written; nothing asks to be read or
// approved again. Each row opens with its subject's emoji; who took it follows, in the status language: the developer
// in the human tone, with an icon and a word, an agent in plain words. A row links to the tasks it led to, and a task
// links back to its decision (Task.decision). An answer in a wish without a lead session says no lead was told. The
// latest decisions show, the older ones fold behind a line (older.tsx); a long note folds under its first line, and
// its body is rendered once opened.
import { Bot, CornerDownRight } from "lucide-react";
import {
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useRef,
  useState,
} from "react";

import { type Decision, BY_LEAD } from "./data/decisions";
import { answerText, when } from "./data/format";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { OlderLine, useRecent } from "./older";
import { StatusBadge } from "./status";
import { SettingsDrawer } from "./wish-settings";

import "./decision-log.css";

// A note or a block this long, in characters or lines, is folded under its first line.
const LONG_NOTE = 280;
const LONG_NOTE_LINES = 6;

export function DecisionLog<T extends { item: Decision }>({
  items,
  origin,
  noLead,
  focus = "",
  onTask,
  workspace = false,
  compact = false,
  visible = true,
}: {
  items: readonly T[];
  // Where a decision comes from, in the flight plan of several wishes: its wish.
  origin?: (item: T) => ReactNode;
  // The decision's wish has no lead session: its answer told no lead.
  noLead?: (item: T) => boolean;
  // The decision to bring into sight: its id.
  focus?: string;
  // Opens a task in the Tasks tab.
  onTask: (taskId: string) => void;
  // Workspace views use flat rows and open the complete decision in the native settings drawer.
  workspace?: boolean;
  compact?: boolean;
  // A workspace deep-link opens its drawer only once its activity panel and tab are visible.
  visible?: boolean;
}) {
  const [detail, setDetail] = useState<T>();
  const [closeRequest, setCloseRequest] = useState(0);
  const pendingTask = useRef<(() => void) | null>(null);
  // A decision a task leads back to shows, folded or not.
  const { shown, more } = useRecent(
    items.length,
    focus ? items.findIndex((item) => item.item.id === focus) : -1,
  );
  useEffect(() => {
    if (workspace && !visible) {
      setDetail(undefined);
      return;
    }
    if (!focus) return;
    const item = items.find((candidate) => candidate.item.id === focus);
    document
      .getElementById(`decision-${focus}`)
      ?.scrollIntoView({ block: "center" });
    if (workspace && item) setDetail(item);
  }, [focus, items, visible, workspace]);
  const openTask = (taskId: string) => {
    if (!workspace) {
      onTask(taskId);
      return;
    }
    // A compact row exposes task links without mounting its detail drawer. Only defer a route when that drawer is
    // actually open; otherwise waiting for its close callback leaves the link inert forever.
    if (!detail) {
      onTask(taskId);
      return;
    }
    pendingTask.current = () => onTask(taskId);
    setCloseRequest((request) => request + 1);
  };
  return (
    <section
      className={`wish-section decision-log${workspace ? " is-workspace" : ""}${compact ? " is-compact" : ""}`}
      role="tabpanel"
      aria-labelledby="view-tab-decisions"
      aria-label={t("tabs.decisions")}
    >
      <div className="section-title">
        <h2>
          {t("tabs.decisions")}
          <span className="count">{items.length}</span>
        </h2>
        <p>{t("decision.detail")}</p>
      </div>
      {items.length === 0 && <p className="muted-text">{t("decision.none")}</p>}
      {items.length > 0 && (
        <div className="card-grid">
          {items.slice(0, shown).map((item) => (
            <DecisionRow
              key={item.item.id}
              decision={item.item}
              origin={origin?.(item)}
              noLead={!!noLead?.(item)}
              focused={item.item.id === focus}
              workspace={workspace}
              compact={compact}
              onOpenDetail={workspace ? () => setDetail(item) : undefined}
              onTask={openTask}
            />
          ))}
        </div>
      )}
      <OlderLine
        hidden={items.length - shown}
        label={(count, hidden) =>
          count < hidden
            ? t("decision.older_some", { count, hidden })
            : t("decision.older", { count })
        }
        onShow={more}
      />
      {detail && workspace && (
        <SettingsDrawer
          title={decisionLabel(detail.item)}
          closeRequest={closeRequest}
          onClose={() => setDetail(undefined)}
          onClosed={() => {
            const action = pendingTask.current;
            pendingTask.current = null;
            if (!action) return;
            action();
            return false;
          }}
        >
          <DecisionDetail
            decision={detail.item}
            origin={origin?.(detail)}
            noLead={!!noLead?.(detail)}
            onTask={openTask}
          />
        </SettingsDrawer>
      )}
    </section>
  );
}

// Who says who took a decision: the developer, in the human tone; an agent, the lead or a worker, in plain words.
export function Who({ decision: d }: { decision: Decision }) {
  if (d.human)
    return (
      <StatusBadge
        tone="human"
        label={t(d.approved ? "decision.approved_by_you" : "decision.by_you")}
      />
    );
  return (
    <span className="decision-by">
      <Bot size={13} aria-hidden="true" />
      {d.by === BY_LEAD
        ? t("decision.by_lead")
        : t("decision.by_task", { task: d.by })}
    </span>
  );
}

function DecisionRow({
  decision: d,
  origin,
  noLead,
  focused,
  workspace,
  compact,
  onOpenDetail,
  onTask,
}: {
  decision: Decision;
  origin?: ReactNode;
  noLead: boolean;
  focused: boolean;
  workspace: boolean;
  compact: boolean;
  onOpenDetail?: () => void;
  onTask: (taskId: string) => void;
}) {
  const q = d.question;
  const title = q?.text || d.block?.title || "";
  const note = q ? (q.answer?.note ?? "") : (d.block?.content ?? "");
  const long =
    note.length > LONG_NOTE || note.split("\n").length > LONG_NOTE_LINES;
  const interactive = workspace && !!onOpenDetail;
  const openFromKeyboard = (event: KeyboardEvent<HTMLElement>) => {
    if (
      !interactive ||
      event.target !== event.currentTarget ||
      (event.key !== "Enter" && event.key !== " ")
    )
      return;
    event.preventDefault();
    onOpenDetail?.();
  };
  return (
    <article
      className={`decision-row ${d.human ? "tone-human" : "agent"} ${focused ? "focused" : ""}${interactive ? " is-detail-trigger" : ""}${compact ? " is-compact" : ""}`}
      id={`decision-${d.id}`}
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-label={interactive ? t("page.details") : undefined}
      onClick={interactive ? onOpenDetail : undefined}
      onKeyDown={openFromKeyboard}
    >
      <span className="decision-icon" aria-hidden="true">
        {d.icon}
      </span>
      <div className="decision-main">
        <div className="decision-meta">
          {origin}
          <Who decision={d} />
          {q && <span className="agent-code">{q.code}</span>}
          <time>{when(d.at)}</time>
        </div>
        <h3>{title}</h3>
        {q && <p className="answer-value">→ {answerText(q)}</p>}
        {q && noLead && (
          <p className="no-lead">{t("question.no_lead_answered")}</p>
        )}
        {note &&
          (workspace ? (
            <p className="decision-note decision-note-preview">
              {firstLine(note)}
            </p>
          ) : long ? (
            <FoldedNote note={note} />
          ) : (
            <div className="decision-note prose">
              <MarkdownBody text={note} />
            </div>
          ))}
        {d.tasks.length > 0 && (
          <p className="decision-tasks">
            <CornerDownRight size={13} aria-hidden="true" />
            {t("decision.led_to")}
            {d.tasks.map((task) => (
              <a
                key={task.id}
                className="agent-code"
                href={`#task-${task.id}`}
                title={t("decision.open_task", { task: task.code })}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  onTask(task.id);
                }}
              >
                {task.code}
              </a>
            ))}
          </p>
        )}
      </div>
    </article>
  );
}

function decisionLabel(d: Decision): string {
  return d.question?.code || d.block?.title || t("tabs.decisions");
}

function DecisionDetail({
  decision: d,
  origin,
  noLead,
  onTask,
}: {
  decision: Decision;
  origin?: ReactNode;
  noLead: boolean;
  onTask: (taskId: string) => void;
}) {
  const q = d.question;
  const title = q?.text || d.block?.title || "";
  const note = q ? (q.answer?.note ?? "") : (d.block?.content ?? "");
  return (
    <div className="workspace-activity-detail decision-detail">
      <div className="decision-meta">
        {origin}
        <Who decision={d} />
        {q && <span className="agent-code">{q.code}</span>}
        <time>{when(d.at)}</time>
      </div>
      <h3>{title}</h3>
      {q && <p className="answer-value">→ {answerText(q)}</p>}
      {q && noLead && (
        <p className="no-lead">{t("question.no_lead_answered")}</p>
      )}
      {note && (
        <div className="decision-note prose">
          <MarkdownBody text={note} />
        </div>
      )}
      {d.tasks.length > 0 && (
        <p className="decision-tasks">
          <CornerDownRight size={13} aria-hidden="true" />
          {t("decision.led_to")}
          {d.tasks.map((task) => (
            <a
              key={task.id}
              className="agent-code"
              href={`#task-${task.id}`}
              title={t("decision.open_task", { task: task.code })}
              onClick={(event) => {
                event.preventDefault();
                onTask(task.id);
              }}
            >
              {task.code}
            </a>
          ))}
        </p>
      )}
    </div>
  );
}

// FoldedNote is a long note under its first line: its Markdown is rendered once opened, not for every decision.
function FoldedNote({ note }: { note: string }) {
  const [open, setOpen] = useState(false);
  return (
    <details
      className="decision-note"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>{firstLine(note)}</summary>
      {open && (
        <div className="prose">
          <MarkdownBody text={note} />
        </div>
      )}
    </details>
  );
}

// DecisionLink is a task's way back to the decision it comes from: its emoji and its code or title.
export function DecisionLink({
  decision: d,
  onOpen,
}: {
  decision: Decision;
  onOpen: () => void;
}) {
  const label = d.question?.code || d.block?.title || "";
  return (
    <a
      className="decision-link"
      href={`#decision-${d.id}`}
      title={t("decision.open", { decision: label })}
      onClick={(event) => {
        event.preventDefault();
        onOpen();
      }}
    >
      <span aria-hidden="true">{d.icon}</span>
      {t("decision.from", { decision: label })}
    </a>
  );
}

// firstLine is a note's first line of text, for its folded summary.
function firstLine(text: string): string {
  const line =
    text
      .split("\n")
      .map((l) => l.replace(/^[#>*+\-\s]+/, "").trim())
      .find(Boolean) ?? "";
  return line.length > 140 ? `${line.slice(0, 139)}…` : line;
}

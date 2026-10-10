// The decision log, a tab of its own in a wish's view and in the flight plan: every decision taken, the latest first,
// read only. No button: an answered question was answered, a decision block was written; nothing asks to be read or
// approved again. Each row opens with its subject's emoji; who took it follows, in the status language: the developer
// in the human tone, with an icon and a word, an agent in plain words. A row links to the tasks it led to, and a task
// links back to its decision (Task.decision). An answer in a wish without a lead session says no lead was told.
import { Bot, CornerDownRight } from "lucide-react";
import { type ReactNode, useEffect } from "react";

import { type Decision, BY_LEAD } from "./data/decisions";
import { answerText, when } from "./data/format";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";

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
}) {
  useEffect(() => {
    if (focus)
      document
        .getElementById(`decision-${focus}`)
        ?.scrollIntoView({ block: "center" });
  }, [focus]);
  return (
    <section
      className="wish-section decision-log"
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
          {items.map((item) => (
            <DecisionRow
              key={item.item.id}
              decision={item.item}
              origin={origin?.(item)}
              noLead={!!noLead?.(item)}
              focused={item.item.id === focus}
              onTask={onTask}
            />
          ))}
        </div>
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
  onTask,
}: {
  decision: Decision;
  origin?: ReactNode;
  noLead: boolean;
  focused: boolean;
  onTask: (taskId: string) => void;
}) {
  const q = d.question;
  const title = q?.text || d.block?.title || "";
  const note = q ? (q.answer?.note ?? "") : (d.block?.content ?? "");
  const long =
    note.length > LONG_NOTE || note.split("\n").length > LONG_NOTE_LINES;
  return (
    <article
      className={`decision-row ${d.human ? "tone-human" : "agent"} ${focused ? "focused" : ""}`}
      id={`decision-${d.id}`}
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
          (long ? (
            <details className="decision-note">
              <summary>{firstLine(note)}</summary>
              <div className="prose">
                <MarkdownBody text={note} />
              </div>
            </details>
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

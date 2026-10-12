// A wish's Review tab: its merged work, a line per task, checked once its diff was read. The merges Djinn started sit
// under the task they serve (src/data/review.ts). A click on a line opens its diff in place, with the comments on its
// lines; the box checks it, for good: the mark is kept on the task (MarkService.Put), with who checked it and when.
// What is checked folds at the end; the tab's count is what is left.
import { CheckSquare, MessageSquare, Square } from "lucide-react";
import { memo, useState } from "react";

import type { Task } from "../gen/ts/plan/v1/plan_pb";
import { type ReviewRow, reviewedMark, rowReviewed } from "./data/review";
import { when } from "./data/format";
import { TaskDiffView } from "./diff";
import { t } from "./i18n";
import { Fold } from "./task-tabs";

import "./review-tab.css";

// The developer, as a mark names them (internal/plan's actor).
const LOCAL = "local";

export function ReviewTab({
  rows,
  fold,
  codes,
  onReview,
}: {
  rows: readonly ReviewRow[];
  // The key of the fold: the wish's id.
  fold: string;
  codes: ReadonlyMap<string, string>;
  // Checks a task reviewed, or takes the mark off.
  onReview: (task: Task, remove: boolean) => Promise<unknown>;
}) {
  const [open, setOpen] = useState("");
  const toggle = (id: string) => setOpen((x) => (x === id ? "" : id));
  const line = (task: Task, merge: boolean) => (
    <ReviewLine
      key={task.id}
      task={task}
      merge={merge}
      open={open === task.id}
      codes={codes}
      onOpen={toggle}
      onReview={onReview}
    />
  );
  const row = (r: ReviewRow) => (
    <li key={r.task.id} className="review-row">
      {line(r.task, false)}
      {r.merges.length > 0 && (
        <ul className="review-merges">
          {r.merges.map((x) => (
            <li key={x.id}>{line(x, true)}</li>
          ))}
        </ul>
      )}
    </li>
  );
  const left = rows.filter((r) => !rowReviewed(r));
  const done = rows.filter(rowReviewed);
  return (
    <div
      role="tabpanel"
      aria-labelledby="view-tab-review"
      className="review-tab"
    >
      {rows.length === 0 && <p className="muted-text">{t("review.none")}</p>}
      {left.length > 0 && <ul className="review-list">{left.map(row)}</ul>}
      <Fold
        id={`reviewed:${fold}`}
        count={done.length}
        labelHidden={t("review.show_reviewed", { count: done.length })}
        labelShown={t("review.hide_reviewed")}
      >
        <ul className="review-list">{done.map(row)}</ul>
      </Fold>
    </div>
  );
}

export const ReviewLine = memo(function ReviewLine({
  task,
  merge,
  open,
  codes,
  onOpen,
  onReview,
}: {
  task: Task;
  merge: boolean;
  open: boolean;
  codes: ReadonlyMap<string, string>;
  onOpen: (id: string) => void;
  onReview: (task: Task, remove: boolean) => Promise<unknown>;
}) {
  const [sending, setSending] = useState(false);
  const mark = reviewedMark(task);
  const comments = task.comments ?? [];
  const who = !mark
    ? ""
    : mark.actor === LOCAL
      ? t("review.you")
      : (codes.get(mark.actor) ?? mark.actor);
  return (
    <div
      className={`review-item${mark ? " reviewed" : ""}${open ? " open" : ""}`}
      id={`review-${task.id}`}
    >
      <div className="review-line">
        <button
          type="button"
          role="checkbox"
          className="review-check"
          aria-checked={!!mark}
          disabled={sending}
          aria-label={t(mark ? "review.unmark" : "review.mark", {
            task: task.code,
          })}
          title={t(mark ? "review.unmark" : "review.mark", { task: task.code })}
          onClick={() => {
            setSending(true);
            void onReview(task, !!mark)
              .catch(() => {})
              .finally(() => setSending(false));
          }}
        >
          {mark ? <CheckSquare size={15} /> : <Square size={15} />}
        </button>
        <button
          type="button"
          className="review-open"
          aria-expanded={open}
          title={t("review.open", { task: task.code })}
          onClick={() => onOpen(task.id)}
        >
          <span className="review-code">{task.code}</span>
          {merge && <span className="review-merge">{t("task.merge")}</span>}
          <span className="review-title">{task.title}</span>
        </button>
        {comments.length > 0 && (
          <span
            className="review-comments"
            title={t("review.comments", { count: comments.length })}
          >
            <MessageSquare size={12} aria-hidden="true" />
            {comments.length}
          </span>
        )}
        {mark && (
          <span className="review-by">
            {t("review.by", { who, when: when(mark.createTime) })}
          </span>
        )}
      </div>
      {open && (
        <div className="review-body">
          {comments.length > 0 && (
            <ul className="review-comment-list">
              {comments.map((c) => (
                <li key={c.id} className={c.resolved ? "resolved" : ""}>
                  <span className="review-where">
                    {c.file}:{c.line}
                    {c.endLine > c.line ? `-${c.endLine}` : ""}
                  </span>
                  <span>{c.text}</span>
                </li>
              ))}
            </ul>
          )}
          <TaskDiffView task={task} />
        </div>
      )}
    </div>
  );
});

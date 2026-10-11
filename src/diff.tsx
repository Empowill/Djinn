// TaskDiffView displays git diffs for a task: changed files list, then unified and side-by-side diff viewers. A line
// of the unified diff, or a range of them (shift-click), takes a review comment: TaskService.Comment.
import { type FormEvent, memo, useEffect, useState } from "react";
import {
  DiffHunk,
  DiffLine,
  DiffLineKind,
  type Task,
  type TaskComment,
  TaskDiffFile,
  TaskFileDiff,
  type TaskServiceDiffResponse,
} from "../gen/ts/plan/v1/plan_pb";
import { useDjinn } from "./data/djinn";
import { t } from "./i18n";
import "./diff.css";

// Map Chroma token categories to CSS syntax classes.
const tokenClassMap: Record<string, string> = {
  keyword: "tok-keyword",
  string: "tok-string",
  number: "tok-number",
  comment: "tok-comment",
  function: "tok-function",
  type: "tok-type",
  operator: "tok-operator",
  variable: "tok-variable",
};

// LineTokens renders syntax-highlighted tokens returned by Go Chroma server.
function LineTokens({ line }: { line: DiffLine }) {
  if (!line.tokens || line.tokens.length === 0) {
    return <>{line.text || "\u00A0"}</>;
  }
  return (
    <>
      {line.tokens.map((tok, i) => {
        const cls = tok.category ? tokenClassMap[tok.category] : undefined;
        return (
          <span key={i} className={cls}>
            {tok.text}
          </span>
        );
      })}
    </>
  );
}

interface SplitRow {
  left?: DiffLine;
  right?: DiffLine;
}

// pairHunkLines pairs deleted and added lines for side-by-side display.
function pairHunkLines(lines: readonly DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.kind === DiffLineKind.CONTEXT) {
      rows.push({ left: line, right: line });
      i++;
    } else {
      const dels: DiffLine[] = [];
      while (i < lines.length && lines[i].kind === DiffLineKind.DELETED) {
        dels.push(lines[i]);
        i++;
      }
      const adds: DiffLine[] = [];
      while (i < lines.length && lines[i].kind === DiffLineKind.ADDED) {
        adds.push(lines[i]);
        i++;
      }
      const count = Math.max(dels.length, adds.length);
      for (let k = 0; k < count; k++) {
        rows.push({
          left: k < dels.length ? dels[k] : undefined,
          right: k < adds.length ? adds[k] : undefined,
        });
      }
    }
  }
  return rows;
}

export interface CommentBoxProps {
  startLine: number;
  endLine: number;
  taskId?: string;
  filePath?: string;
  commit?: string;
  onCancel: () => void;
  onCommentAdded?: (comment: TaskComment) => void;
}

export function CommentBox({
  startLine,
  endLine,
  taskId,
  filePath,
  commit,
  onCancel,
  onCommentAdded,
}: CommentBoxProps) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const [text, setText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const title =
    startLine === endLine
      ? t("diff.comment_on_line", { line: startLine })
      : t("diff.comment_on_lines", { start: startLine, end: endLine });

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = text.trim();
    if (!trimmed || submitting) return;
    if (!clients || !taskId) return;
    setSubmitting(true);
    setError(null);
    try {
      const res = await clients.tasks.comment({
        taskId,
        text: trimmed,
        file: filePath ?? "",
        line: startLine,
        endLine: endLine,
        commit: commit ?? "",
      });
      if (res.comment) {
        onCommentAdded?.(res.comment);
      }
      onCancel();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      setSubmitting(false);
    }
  };

  return (
    <form className="diff-comment-box" onSubmit={handleSubmit}>
      <div className="diff-comment-box-title">{title}</div>
      <textarea
        className="diff-comment-textarea"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={t("diff.comment_placeholder")}
        aria-label={title}
        autoFocus
        rows={3}
      />
      {error && <div className="diff-error">{error}</div>}
      <div className="diff-comment-box-actions">
        <button
          type="button"
          className="diff-comment-btn-cancel"
          onClick={onCancel}
          disabled={submitting}
        >
          {t("diff.comment_cancel")}
        </button>
        <button
          type="submit"
          className="diff-comment-btn-submit"
          disabled={submitting || !text.trim() || !clients || !taskId}
        >
          {t("diff.comment_submit")}
        </button>
      </div>
    </form>
  );
}

export interface UnifiedViewProps {
  hunks: readonly DiffHunk[];
  comments?: readonly TaskComment[];
  taskId?: string;
  filePath?: string;
  headCommit?: string;
  onCommentAdded?: (comment: TaskComment) => void;
}

export function UnifiedView({
  hunks,
  comments,
  taskId,
  filePath,
  headCommit,
  onCommentAdded,
}: UnifiedViewProps) {
  const [activeRange, setActiveRange] = useState<{
    start: number;
    end: number;
  } | null>(null);

  const handleLineClick = (lineNum: number, event: React.MouseEvent) => {
    if (lineNum <= 0) return;
    if (event.shiftKey && activeRange) {
      setActiveRange({
        start: Math.min(activeRange.start, lineNum),
        end: Math.max(activeRange.start, lineNum),
      });
    } else {
      setActiveRange({ start: lineNum, end: lineNum });
    }
  };

  return (
    <div className="diff-container">
      {hunks.map((hunk, hIdx) => (
        <div key={hIdx} className="diff-hunk">
          <div className="diff-hunk-header">{hunk.header}</div>
          <div className="diff-lines-unified">
            {hunk.lines.map((line, lIdx) => {
              // A comment names a line of the task's version of the file: a deleted line has none.
              const lineNum = line.newLine;
              const isSelected =
                activeRange !== null &&
                lineNum > 0 &&
                lineNum >= activeRange.start &&
                lineNum <= activeRange.end;
              const rowClass =
                (line.kind === DiffLineKind.ADDED
                  ? "line-added"
                  : line.kind === DiffLineKind.DELETED
                    ? "line-deleted"
                    : "line-context") + (isSelected ? " line-selected" : "");
              const marker =
                line.kind === DiffLineKind.ADDED
                  ? "+"
                  : line.kind === DiffLineKind.DELETED
                    ? "-"
                    : " ";

              const lineComments = (comments ?? []).filter((c) => {
                if (lineNum <= 0) return false;
                if (c.file && filePath && c.file !== filePath) return false;
                const end = c.endLine > 0 ? c.endLine : c.line;
                return end === lineNum;
              });

              const showCommentBox =
                activeRange !== null && activeRange.end === lineNum;

              return (
                <div key={lIdx} style={{ display: "contents" }}>
                  <div
                    className={`diff-line-row ${rowClass}`}
                    onClick={(e) => handleLineClick(lineNum, e)}
                  >
                    <span className="diff-cell-num">
                      {line.oldLine > 0 ? line.oldLine : ""}
                    </span>
                    <span className="diff-cell-num">
                      {line.newLine > 0 ? line.newLine : ""}
                    </span>
                    <span className="diff-cell-marker">{marker}</span>
                    <span className="diff-cell-code">
                      {lineNum > 0 && (
                        <button
                          type="button"
                          className="diff-add-comment-btn"
                          title={t("diff.add_comment")}
                          aria-label={t("diff.add_comment")}
                          onClick={(e) => {
                            e.stopPropagation();
                            setActiveRange({ start: lineNum, end: lineNum });
                          }}
                        >
                          +
                        </button>
                      )}
                      <LineTokens line={line} />
                    </span>
                  </div>

                  {lineComments.map((comment, cIdx) => (
                    <div
                      key={comment.id || `c-${lineNum}-${cIdx}`}
                      className="diff-comment-row"
                    >
                      <span className="diff-cell-num" />
                      <span className="diff-cell-num" />
                      <span className="diff-cell-marker" />
                      <div className="diff-cell-code diff-comment-cell">
                        <div
                          className={`diff-comment-item ${comment.resolved ? "resolved" : ""}`}
                        >
                          {comment.resolved && (
                            <span className="diff-comment-resolved-badge">
                              {t("diff.resolved")}
                            </span>
                          )}
                          <div className="diff-comment-text">
                            {comment.text}
                          </div>
                        </div>
                      </div>
                    </div>
                  ))}

                  {showCommentBox && (
                    <div className="diff-comment-row">
                      <span className="diff-cell-num" />
                      <span className="diff-cell-num" />
                      <span className="diff-cell-marker" />
                      <div className="diff-cell-code diff-comment-cell">
                        <CommentBox
                          startLine={activeRange.start}
                          endLine={activeRange.end}
                          taskId={taskId}
                          filePath={filePath}
                          commit={headCommit}
                          onCancel={() => setActiveRange(null)}
                          onCommentAdded={(newComment) => {
                            setActiveRange(null);
                            onCommentAdded?.(newComment);
                          }}
                        />
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}

export interface SplitViewProps {
  hunks: readonly DiffHunk[];
  comments?: readonly TaskComment[];
  taskId?: string;
  filePath?: string;
  headCommit?: string;
  onCommentAdded?: (comment: TaskComment) => void;
}

export function SplitView({ hunks }: SplitViewProps) {
  return (
    <div className="diff-container">
      {hunks.map((hunk, hIdx) => {
        const rows = pairHunkLines(hunk.lines);
        return (
          <div key={hIdx} className="diff-hunk">
            <div className="diff-hunk-header">{hunk.header}</div>
            <div className="diff-split-table">
              {rows.map((row, rIdx) => {
                const leftClass = row.left
                  ? row.left.kind === DiffLineKind.DELETED
                    ? "line-deleted"
                    : "line-context"
                  : "line-empty";
                const rightClass = row.right
                  ? row.right.kind === DiffLineKind.ADDED
                    ? "line-added"
                    : "line-context"
                  : "line-empty";
                const leftMarker = row.left
                  ? row.left.kind === DiffLineKind.DELETED
                    ? "-"
                    : " "
                  : " ";
                const rightMarker = row.right
                  ? row.right.kind === DiffLineKind.ADDED
                    ? "+"
                    : " "
                  : " ";

                return (
                  <div key={rIdx} className="diff-split-row">
                    <div className={`diff-split-side left ${leftClass}`}>
                      <div className="diff-split-inner">
                        <span className="diff-cell-num">
                          {row.left && row.left.oldLine > 0
                            ? row.left.oldLine
                            : ""}
                        </span>
                        <span className="diff-cell-marker">{leftMarker}</span>
                        <span className="diff-cell-code">
                          {row.left ? <LineTokens line={row.left} /> : "\u00A0"}
                        </span>
                      </div>
                    </div>
                    <div className={`diff-split-side right ${rightClass}`}>
                      <div className="diff-split-inner">
                        <span className="diff-cell-num">
                          {row.right && row.right.newLine > 0
                            ? row.right.newLine
                            : ""}
                        </span>
                        <span className="diff-cell-marker">{rightMarker}</span>
                        <span className="diff-cell-code">
                          {row.right ? (
                            <LineTokens line={row.right} />
                          ) : (
                            "\u00A0"
                          )}
                        </span>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function SingleFileDiff({
  task,
  file,
  viewMode,
  onToggleMode,
  comments,
  onCommentAdded,
}: {
  task: Task;
  file: TaskDiffFile;
  viewMode: "unified" | "split";
  onToggleMode: (mode: "unified" | "split") => void;
  comments: readonly TaskComment[];
  onCommentAdded: (comment: TaskComment) => void;
}) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const [fileDiff, setFileDiff] = useState<TaskFileDiff | null>(null);
  const [headCommit, setHeadCommit] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    if (!clients) {
      setLoading(false);
      return;
    }
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: file.path,
      })
      .then((res: TaskServiceDiffResponse) => {
        if (!active) return;
        setFileDiff(res.file ?? null);
        setHeadCommit(res.head);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!active) return;
        const msg = err instanceof Error ? err.message : String(err);
        setError(msg);
        setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [clients, task.id, file.path]);

  return (
    <div className="single-file-diff">
      <div className="diff-toolbar">
        <span className="diff-toolbar-title">
          {file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}
        </span>
        <div className="diff-mode-toggle">
          <button
            type="button"
            className={`diff-mode-btn ${viewMode === "unified" ? "active" : ""}`}
            onClick={() => onToggleMode("unified")}
          >
            {t("diff.unified")}
          </button>
          <button
            type="button"
            className={`diff-mode-btn ${viewMode === "split" ? "active" : ""}`}
            onClick={() => onToggleMode("split")}
          >
            {t("diff.split")}
          </button>
        </div>
      </div>

      {loading && <div className="diff-loading">{t("diff.loading")}</div>}
      {error && <div className="diff-error">{t("diff.error", { error })}</div>}
      {!loading && !error && fileDiff?.tooLarge && (
        <div className="diff-too-large">
          <p className="diff-too-large-msg">
            {t("diff.too_large", { count: fileDiff.totalLines })}
          </p>
          <p className="diff-too-large-cmd-label">{t("diff.too_large_cmd")}</p>
          <pre className="diff-too-large-cmd">
            <code>{fileDiff.command}</code>
          </pre>
        </div>
      )}
      {!loading && !error && fileDiff?.binary && (
        <div className="diff-binary-msg">{t("diff.binary_notice")}</div>
      )}
      {!loading && !error && !fileDiff?.tooLarge && !fileDiff?.binary && (
        <>
          {viewMode === "unified" ? (
            <UnifiedView
              hunks={fileDiff?.hunks ?? []}
              comments={comments}
              taskId={task.id}
              filePath={file.path}
              headCommit={headCommit}
              onCommentAdded={onCommentAdded}
            />
          ) : (
            <SplitView
              hunks={fileDiff?.hunks ?? []}
              comments={comments}
              taskId={task.id}
              filePath={file.path}
              headCommit={headCommit}
              onCommentAdded={onCommentAdded}
            />
          )}
        </>
      )}
    </div>
  );
}

export const TaskDiffView = memo(function TaskDiffView({
  task,
}: {
  task: Task;
}) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [files, setFiles] = useState<TaskDiffFile[]>([]);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<"unified" | "split">("unified");
  const [localComments, setLocalComments] = useState<readonly TaskComment[]>(
    task.comments ?? [],
  );

  useEffect(() => {
    setLocalComments(task.comments ?? []);
  }, [task.comments]);

  const handleCommentAdded = (newComment: TaskComment) => {
    setLocalComments((prev) => [...prev, newComment]);
  };

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    if (!clients) {
      setLoading(false);
      return;
    }
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: "",
      })
      .then((res: TaskServiceDiffResponse) => {
        if (!active) return;
        setFiles(res.files);
        setLoading(false);
        if (res.files.length > 0) {
          const firstVisible =
            res.files.find((f: TaskDiffFile) => !f.folded) ?? res.files[0];
          setSelectedPath(firstVisible.path);
        }
      })
      .catch((err: unknown) => {
        if (!active) return;
        const msg = err instanceof Error ? err.message : String(err);
        setError(msg);
        setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [clients, task.id]);

  if (loading) {
    return (
      <div className="task-diff-view">
        <div className="diff-loading">{t("diff.loading")}</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="task-diff-view">
        <div className="diff-error">{t("diff.error", { error })}</div>
      </div>
    );
  }

  if (files.length === 0) {
    return (
      <div className="task-diff-view">
        <div className="diff-empty">{t("diff.no_changes")}</div>
      </div>
    );
  }

  const selectedFile = files.find((f) => f.path === selectedPath) ?? files[0];

  return (
    <div className="task-diff-view">
      <div className="diff-file-list" role="tablist">
        {files.map((file) => (
          <button
            key={file.path}
            type="button"
            role="tab"
            aria-selected={file.path === selectedPath}
            className={`diff-file-btn ${file.path === selectedPath ? "active" : ""}`}
            onClick={() => setSelectedPath(file.path)}
          >
            <span
              className="diff-file-path"
              title={
                file.oldPath ? `${file.oldPath} → ${file.path}` : file.path
              }
            >
              {file.path}
            </span>
            <span className="diff-file-counts">
              {file.added > 0 && (
                <span className="diff-count-add">+{file.added}</span>
              )}
              {file.deleted > 0 && (
                <span className="diff-count-del">-{file.deleted}</span>
              )}
            </span>
            <span className="diff-file-flags">
              {file.binary && (
                <span className="diff-flag">{t("diff.binary")}</span>
              )}
              {file.generated && (
                <span className="diff-flag">{t("diff.generated")}</span>
              )}
              {file.lock && <span className="diff-flag">{t("diff.lock")}</span>}
            </span>
          </button>
        ))}
      </div>

      {selectedFile && (
        <SingleFileDiff
          task={task}
          file={selectedFile}
          viewMode={viewMode}
          onToggleMode={setViewMode}
          comments={localComments}
          onCommentAdded={handleCommentAdded}
        />
      )}
    </div>
  );
});

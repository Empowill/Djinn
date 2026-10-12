// TaskDiffView displays git diffs for a task: changed files list, then unified and side-by-side diff viewers. A file's
// diff comes by chunks of 2 000 lines, the next one asked for as the reader scrolls near the end. A line of the unified
// diff, or a range of them (shift-click), takes a review comment: TaskService.Comment.
import {
  type FormEvent,
  memo,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
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

interface LineRange {
  start: number;
  end: number;
}

// UnifiedLine is a row of the unified diff, its comments and, on the last line of the range picked, the comment box.
// Memoized: a chunk appended to a long diff renders its own rows, not the ones above.
const UnifiedLine = memo(function UnifiedLine({
  line,
  comments,
  selected,
  box,
  taskId,
  filePath,
  headCommit,
  onClick,
  onPick,
  onCancel,
  onAdded,
}: {
  line: DiffLine;
  comments?: readonly TaskComment[];
  selected: boolean;
  // The range picked, given to the row that ends it only.
  box: LineRange | null;
  taskId?: string;
  filePath?: string;
  headCommit?: string;
  onClick: (lineNum: number, shift: boolean) => void;
  onPick: (lineNum: number) => void;
  onCancel: () => void;
  onAdded: (comment: TaskComment) => void;
}) {
  // A comment names a line of the task's version of the file: a deleted line has none.
  const lineNum = line.newLine;
  const rowClass =
    (line.kind === DiffLineKind.ADDED
      ? "line-added"
      : line.kind === DiffLineKind.DELETED
        ? "line-deleted"
        : "line-context") + (selected ? " line-selected" : "");
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

  return (
    <div style={{ display: "contents" }}>
      <div
        className={`diff-line-row ${rowClass}`}
        onClick={(e) => onClick(lineNum, e.shiftKey)}
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
                onPick(lineNum);
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
              <div className="diff-comment-text">{comment.text}</div>
            </div>
          </div>
        </div>
      ))}

      {box && (
        <div className="diff-comment-row">
          <span className="diff-cell-num" />
          <span className="diff-cell-num" />
          <span className="diff-cell-marker" />
          <div className="diff-cell-code diff-comment-cell">
            <CommentBox
              startLine={box.start}
              endLine={box.end}
              taskId={taskId}
              filePath={filePath}
              commit={headCommit}
              onCancel={onCancel}
              onCommentAdded={onAdded}
            />
          </div>
        </div>
      )}
    </div>
  );
});

export function UnifiedView({
  hunks,
  comments,
  taskId,
  filePath,
  headCommit,
  onCommentAdded,
}: UnifiedViewProps) {
  const [activeRange, setActiveRange] = useState<LineRange | null>(null);

  const handleLineClick = useCallback((lineNum: number, shift: boolean) => {
    if (lineNum <= 0) return;
    setActiveRange((range) =>
      shift && range
        ? {
            start: Math.min(range.start, lineNum),
            end: Math.max(range.start, lineNum),
          }
        : { start: lineNum, end: lineNum },
    );
  }, []);
  const pick = useCallback(
    (lineNum: number) => setActiveRange({ start: lineNum, end: lineNum }),
    [],
  );
  const cancel = useCallback(() => setActiveRange(null), []);
  const added = useCallback(
    (comment: TaskComment) => {
      setActiveRange(null);
      onCommentAdded?.(comment);
    },
    [onCommentAdded],
  );

  return (
    <div className="diff-container">
      {hunks.map((hunk, hIdx) => (
        <div key={hIdx} className="diff-hunk">
          <div className="diff-hunk-header">{hunk.header}</div>
          <div className="diff-lines-unified">
            {hunk.lines.map((line, lIdx) => (
              <UnifiedLine
                key={lIdx}
                line={line}
                comments={comments}
                selected={
                  activeRange !== null &&
                  line.newLine > 0 &&
                  line.newLine >= activeRange.start &&
                  line.newLine <= activeRange.end
                }
                box={
                  activeRange !== null && activeRange.end === line.newLine
                    ? activeRange
                    : null
                }
                taskId={taskId}
                filePath={filePath}
                headCommit={headCommit}
                onClick={handleLineClick}
                onPick={pick}
                onCancel={cancel}
                onAdded={added}
              />
            ))}
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

// SplitRow is a row of the side-by-side diff: a deleted or context line on the left, an added or context line on the
// right. Memoized like UnifiedLine.
const SplitRow = memo(function SplitRow({
  left,
  right,
}: {
  left?: DiffLine;
  right?: DiffLine;
}) {
  const leftClass = left
    ? left.kind === DiffLineKind.DELETED
      ? "line-deleted"
      : "line-context"
    : "line-empty";
  const rightClass = right
    ? right.kind === DiffLineKind.ADDED
      ? "line-added"
      : "line-context"
    : "line-empty";
  const leftMarker = left?.kind === DiffLineKind.DELETED ? "-" : " ";
  const rightMarker = right?.kind === DiffLineKind.ADDED ? "+" : " ";

  return (
    <div className="diff-split-row">
      <div className={`diff-split-side left ${leftClass}`}>
        <div className="diff-split-inner">
          <span className="diff-cell-num">
            {left && left.oldLine > 0 ? left.oldLine : ""}
          </span>
          <span className="diff-cell-marker">{leftMarker}</span>
          <span className="diff-cell-code">
            {left ? <LineTokens line={left} /> : "\u00A0"}
          </span>
        </div>
      </div>
      <div className={`diff-split-side right ${rightClass}`}>
        <div className="diff-split-inner">
          <span className="diff-cell-num">
            {right && right.newLine > 0 ? right.newLine : ""}
          </span>
          <span className="diff-cell-marker">{rightMarker}</span>
          <span className="diff-cell-code">
            {right ? <LineTokens line={right} /> : "\u00A0"}
          </span>
        </div>
      </div>
    </div>
  );
});

export function SplitView({ hunks }: SplitViewProps) {
  return (
    <div className="diff-container">
      {hunks.map((hunk, hIdx) => (
        <div key={hIdx} className="diff-hunk">
          <div className="diff-hunk-header">{hunk.header}</div>
          <div className="diff-split-table">
            {pairHunkLines(hunk.lines).map((row, rIdx) => (
              <SplitRow key={rIdx} left={row.left} right={row.right} />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// DiffChunks is a file's diff as far as the reader has scrolled: the hunks of the chunks loaded, a hunk cut between two
// chunks joined back into one.
export interface DiffChunks {
  binary: boolean;
  totalLines: number;
  // Where the next chunk starts; 0 once the file's diff is all loaded.
  nextOffset: number;
  hunks: readonly DiffHunk[];
}

// appendChunk adds a chunk of the file's diff to the ones loaded: a first hunk that carries on the last one goes into
// it. Only the server's patch text is left out, that the window does not show.
export function appendChunk(
  prev: DiffChunks | null,
  file: TaskFileDiff,
): DiffChunks {
  const hunks = prev ? [...prev.hunks] : [];
  for (const hunk of file.hunks) {
    const last = hunks[hunks.length - 1];
    if (hunk.continued && last) {
      hunks[hunks.length - 1] = {
        ...last,
        lines: [...last.lines, ...hunk.lines],
      };
    } else {
      hunks.push(hunk);
    }
  }
  return {
    binary: file.binary,
    totalLines: file.totalLines,
    nextOffset: file.nextOffset,
    hunks,
  };
}

// NextChunk ends a diff that has more to load: it asks for the next chunk as soon as it nears the view, and offers a
// button for when it cannot tell.
function NextChunk({
  offset,
  total,
  loading,
  onLoad,
}: {
  offset: number;
  total: number;
  loading: boolean;
  onLoad: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // A new observer per offset: it reports at once whether the row is still in view after a chunk shorter than the
  // screen.
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) onLoad();
      },
      { rootMargin: "0px 0px 800px 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [offset, onLoad]);
  return (
    <div ref={ref} className="diff-next-chunk">
      <span>{t("diff.more", { shown: offset, total })}</span>
      <button
        type="button"
        className="diff-next-chunk-btn"
        onClick={onLoad}
        disabled={loading}
      >
        {loading ? t("diff.loading") : t("diff.next_chunk")}
      </button>
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
  const [diff, setDiff] = useState<DiffChunks | null>(null);
  const [headCommit, setHeadCommit] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The file the chunks belong to, and whether one is on its way: a chunk of the file left behind is dropped.
  const shown = useRef({ path: "", busy: false });

  useEffect(() => {
    let active = true;
    shown.current = { path: file.path, busy: false };
    setDiff(null);
    setLoading(true);
    setLoadingMore(false);
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
        setDiff(res.file ? appendChunk(null, res.file) : null);
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

  const nextOffset = diff?.nextOffset ?? 0;
  const loadMore = useCallback(() => {
    const path = shown.current.path;
    if (!clients || nextOffset <= 0 || shown.current.busy) return;
    shown.current.busy = true;
    setLoadingMore(true);
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: path,
        offset: nextOffset,
      })
      .then((res: TaskServiceDiffResponse) => {
        if (shown.current.path !== path) return;
        const chunk = res.file;
        if (chunk) setDiff((prev) => appendChunk(prev, chunk));
      })
      .catch((err: unknown) => {
        if (shown.current.path !== path) return;
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (shown.current.path !== path) return;
        shown.current.busy = false;
        setLoadingMore(false);
      });
  }, [clients, task.id, nextOffset]);

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
      {!loading && diff?.binary && (
        <div className="diff-binary-msg">{t("diff.binary_notice")}</div>
      )}
      {!loading && diff && !diff.binary && (
        <>
          {viewMode === "unified" ? (
            <UnifiedView
              hunks={diff.hunks}
              comments={comments}
              taskId={task.id}
              filePath={file.path}
              headCommit={headCommit}
              onCommentAdded={onCommentAdded}
            />
          ) : (
            <SplitView
              hunks={diff.hunks}
              comments={comments}
              taskId={task.id}
              filePath={file.path}
              headCommit={headCommit}
              onCommentAdded={onCommentAdded}
            />
          )}
          {nextOffset > 0 && !error && (
            <NextChunk
              offset={nextOffset}
              total={diff.totalLines}
              loading={loadingMore}
              onLoad={loadMore}
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

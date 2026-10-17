// TaskDiffView displays git diffs for a task: the files it changes as a tree of folders, then their diffs one after the
// other, unified or side by side. A file's diff comes by chunks of 2 000 lines, the next one asked for as the reader
// scrolls near the end. A line of the unified diff, or a range of them (shift-click), takes a review comment:
// TaskService.Comment. Its uncommitted view shows what the task's worktree holds not committed instead, read only.
// TaskReview opens both full size, beside the side panel.
import { ChevronRight, RefreshCw, X } from "lucide-react";
import {
  createContext,
  type FormEvent,
  type KeyboardEvent,
  memo,
  type RefObject,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
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
import {
  type DiffFolder,
  type DiffRow,
  diffFiles,
  diffRows,
  diffTree,
  OPEN_UP_TO,
  rowShowing,
} from "./data/diff-tree";
import { useFullSize } from "./full-size";
import { t } from "./i18n";
import { EmptyState } from "./empty-art";
import { placeTaken, useKeepPlace } from "./scroll-anchor";
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
        onKeyDown={(e) => {
          if (e.key !== "Escape") return;
          e.preventDefault();
          onCancel();
        }}
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
  // No comment on these lines: work not committed has no commit a comment could name.
  readOnly?: boolean;
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
  readOnly,
  onClick,
  onPick,
  onCancel,
  onAdded,
}: {
  line: DiffLine;
  comments?: readonly TaskComment[];
  selected: boolean;
  readOnly?: boolean;
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
          {lineNum > 0 && !readOnly && (
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
  readOnly = false,
}: UnifiedViewProps) {
  const [activeRange, setActiveRange] = useState<LineRange | null>(null);

  const handleLineClick = useCallback(
    (lineNum: number, shift: boolean) => {
      if (lineNum <= 0 || readOnly) return;
      setActiveRange((range) =>
        shift && range
          ? {
              start: Math.min(range.start, lineNum),
              end: Math.max(range.start, lineNum),
            }
          : { start: lineNum, end: lineNum },
      );
    },
    [readOnly],
  );
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
                readOnly={readOnly}
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

// ScrollRootContext is the column the files' diffs scroll in, for what loads as it nears the view; null: the window.
const ScrollRootContext = createContext<HTMLElement | null>(null);

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
  const root = use(ScrollRootContext);
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
      { root, rootMargin: "0px 0px 800px 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [offset, onLoad, root]);
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

// useNear tells, for good, that an element came within a screen of the view of the column it scrolls in; at once
// when asked, or where the browser cannot tell.
function useNear(ref: RefObject<HTMLElement | null>, at: boolean): boolean {
  const root = use(ScrollRootContext);
  const [near, setNear] = useState(
    at || typeof IntersectionObserver === "undefined",
  );
  useEffect(() => {
    const el = ref.current;
    if (near || !el) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setNear(true);
      },
      { root, rootMargin: "100% 0px 100% 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, root, near]);
  return near;
}

// The lines a chunk of a file's diff holds at most: the room a file not loaded yet takes is counted up to it.
const CHUNK = 2000;

const named = (file: TaskDiffFile) =>
  file.oldPath ? `${file.oldPath} → ${file.path}` : file.path;

function Counts({ added, deleted }: { added: number; deleted: number }) {
  return (
    <span className="diff-counts">
      {added > 0 && <span className="diff-count-add">+{added}</span>}
      {deleted > 0 && <span className="diff-count-del">-{deleted}</span>}
    </span>
  );
}

function Flags({ file }: { file: TaskDiffFile }) {
  if (!file.binary && !file.generated && !file.lock) return null;
  return (
    <span className="diff-flags">
      {file.binary && <span className="diff-flag">{t("diff.binary")}</span>}
      {file.generated && (
        <span className="diff-flag">{t("diff.generated")}</span>
      )}
      {file.lock && <span className="diff-flag">{t("diff.lock")}</span>}
    </span>
  );
}

// FileDiff is a file's part of the diff: its name and counts on top, kept in sight while its lines scroll by, then its
// lines. They load as the part nears the view, the first file's at once; a generated, lock or binary file's once the
// reader asks. Until they show, the part takes about the room they will, so a jump to a file below lands on it.
const FileDiff = memo(function FileDiff({
  task,
  uncommitted,
  file,
  first,
  viewMode,
  comments,
  onCommentAdded,
}: {
  task: Task;
  uncommitted: boolean;
  file: TaskDiffFile;
  first: boolean;
  viewMode: "unified" | "split";
  comments: readonly TaskComment[];
  onCommentAdded: (comment: TaskComment) => void;
}) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const part = useRef<HTMLElement>(null);
  const near = useNear(part, first);
  const [asked, setAsked] = useState(false);
  const wanted = near && (!file.folded || asked);
  const [diff, setDiff] = useState<DiffChunks | null>(null);
  const [headCommit, setHeadCommit] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Whether a chunk is on its way: the next one is asked once.
  const busy = useRef(false);

  useEffect(() => {
    if (!wanted || !clients) return;
    let active = true;
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: file.path,
        uncommitted,
      })
      .then((res: TaskServiceDiffResponse) => {
        if (!active) return;
        setDiff(res.file ? appendChunk(null, res.file) : null);
        setHeadCommit(res.head);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!active) return;
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [wanted, clients, task.id, file.path, uncommitted]);

  const nextOffset = diff?.nextOffset ?? 0;
  const loadMore = useCallback(() => {
    if (!clients || nextOffset <= 0 || busy.current) return;
    busy.current = true;
    setLoadingMore(true);
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: file.path,
        offset: nextOffset,
        uncommitted,
      })
      .then((res: TaskServiceDiffResponse) => {
        const chunk = res.file;
        if (chunk) setDiff((prev) => appendChunk(prev, chunk));
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        busy.current = false;
        setLoadingMore(false);
      });
  }, [clients, task.id, file.path, nextOffset, uncommitted]);

  const lines = file.binary ? 1 : file.added + file.deleted;
  return (
    <section
      ref={part}
      className="diff-file"
      data-path={file.path}
      aria-label={named(file)}
    >
      <header className="diff-file-head">
        <span className="diff-file-path" title={named(file)}>
          {named(file)}
        </span>
        <Counts added={file.added} deleted={file.deleted} />
        <Flags file={file} />
        {file.folded && !asked && (
          <button
            type="button"
            className="diff-mode-btn diff-file-show"
            onClick={() => setAsked(true)}
          >
            {t("diff.show_folded")}
          </button>
        )}
      </header>
      {error && <div className="diff-error">{t("diff.error", { error })}</div>}
      {!error && !file.folded && (!wanted || loading) && (
        <div
          className="diff-file-room"
          style={{
            height: `calc(${Math.min(lines, CHUNK) + 2} * var(--diff-line))`,
          }}
        >
          {wanted && <div className="diff-loading">{t("diff.loading")}</div>}
        </div>
      )}
      {wanted && !loading && diff?.binary && (
        <div className="diff-binary-msg">{t("diff.binary_notice")}</div>
      )}
      {wanted && !loading && diff && !diff.binary && (
        <>
          {viewMode === "unified" ? (
            <UnifiedView
              hunks={diff.hunks}
              comments={comments}
              taskId={task.id}
              filePath={file.path}
              headCommit={headCommit}
              onCommentAdded={onCommentAdded}
              readOnly={uncommitted}
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
    </section>
  );
});

// NO_COMMENTS are the comments the uncommitted view shows: those of the task name lines of its commits.
const NO_COMMENTS: readonly TaskComment[] = [];

const keyOf = (row: DiffRow) => `${row.node.kind}:${row.node.path}`;

// TaskDiffView shows a task's diff: the files it changes as a tree of folders on the left (data/diff-tree.ts), their
// diffs one after the other on the right, in the tree's order. A click on a file, or Enter on it, scrolls the diff to
// it; the tree follows the diff as it scrolls, the row of the file read marked. The arrows move in the tree, Right
// and Left open and close a folder, Enter opens. The folders opened or closed stay so while the view lives: another
// read of the diff or the other tab keeps them.
export const TaskDiffView = memo(function TaskDiffView({
  task,
  uncommitted = false,
  reads = 0,
  drawn = false,
}: {
  task: Task;
  // What the task's worktree holds not committed, instead of its committed work.
  uncommitted?: boolean;
  // How many times the diff was read again: each one reads it anew.
  reads?: number;
  // In the drawer, where an empty diff has the room for its drawing (empty-art.tsx); inline in the Review, the line.
  drawn?: boolean;
}) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [noWorktree, setNoWorktree] = useState(false);
  const [files, setFiles] = useState<TaskDiffFile[]>([]);
  const [viewMode, setViewMode] = useState<"unified" | "split">("unified");
  const [localComments, setLocalComments] = useState<readonly TaskComment[]>(
    task.comments ?? [],
  );

  useEffect(() => {
    setLocalComments(task.comments ?? []);
  }, [task.comments]);

  const handleCommentAdded = useCallback((newComment: TaskComment) => {
    setLocalComments((prev) => [...prev, newComment]);
  }, []);

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
        uncommitted,
      })
      .then((res: TaskServiceDiffResponse) => {
        if (!active) return;
        setNoWorktree(res.noWorktree);
        setFiles(res.files);
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
  }, [clients, task.id, uncommitted, reads]);

  const tree = useMemo(() => diffTree(files), [files]);
  const ordered = useMemo(() => diffFiles(tree), [tree]);
  // The folders the reader opened or closed, by path; the others as OPEN_UP_TO says.
  const [folds, setFolds] = useState<ReadonlyMap<string, boolean>>(
    () => new Map(),
  );
  const openAll = files.length <= OPEN_UP_TO;
  const isOpen = useCallback(
    (folder: DiffFolder) => folds.get(folder.path) ?? openAll,
    [folds, openAll],
  );
  const rows = useMemo(() => diffRows(tree, isOpen), [tree, isOpen]);
  // The file read, at the top of the diff: its row, or its closed folder's, is marked.
  const [current, setCurrent] = useState("");
  const showing = rowShowing(rows, current || (ordered[0]?.path ?? ""));
  // The row the keyboard is on: the file read's until a key or a click moves it.
  const [cursor, setCursor] = useState("");
  const found = rows.findIndex((r) => keyOf(r) === cursor);
  const focusAt = found >= 0 ? found : Math.max(showing, 0);

  const treeRef = useRef<HTMLDivElement>(null);
  const columnRef = useRef<HTMLDivElement | null>(null);
  const [column, setColumn] = useState<HTMLDivElement | null>(null);
  const keepPlace = useKeepPlace(columnRef);
  const holdColumn = useCallback(
    (el: HTMLDivElement | null) => {
      setColumn(el);
      return keepPlace(el);
    },
    [keepPlace],
  );
  // The scroll a jump to a file set: the file stays the one read, even where the diff ends before it reaches the top.
  const jumped = useRef(-1);

  const open = useCallback((path: string) => {
    setCurrent(path);
    const col = columnRef.current;
    const part = col
      ? [...col.querySelectorAll<HTMLElement>(".diff-file")].find(
          (p) => p.dataset.path === path,
        )
      : undefined;
    if (!col || !part) return;
    col.scrollTop +=
      part.getBoundingClientRect().top - col.getBoundingClientRect().top;
    jumped.current = col.scrollTop;
    placeTaken(col);
  }, []);

  // The tree follows the scroll: the file read is the last one whose part starts at the top of the column or above.
  useEffect(() => {
    if (!column) return;
    let frame = 0;
    const follow = () => {
      if (Math.abs(column.scrollTop - jumped.current) < 1) return;
      jumped.current = -1;
      const top = column.getBoundingClientRect().top + 2;
      let at = "";
      for (const part of column.querySelectorAll<HTMLElement>(".diff-file")) {
        if (part.getBoundingClientRect().top > top) break;
        at = part.dataset.path ?? "";
      }
      setCurrent(at);
    };
    const onScroll = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(follow);
    };
    column.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      column.removeEventListener("scroll", onScroll);
      cancelAnimationFrame(frame);
    };
  }, [column]);

  // The marked row stays in sight in the tree, which scrolls on its own: nothing around it moves.
  useEffect(() => {
    const box = treeRef.current;
    const row = box?.children[showing];
    if (!box || !(row instanceof HTMLElement)) return;
    if (row.offsetTop < box.scrollTop) box.scrollTop = row.offsetTop;
    else if (
      row.offsetTop + row.offsetHeight >
      box.scrollTop + box.clientHeight
    )
      box.scrollTop = row.offsetTop + row.offsetHeight - box.clientHeight;
  }, [showing]);

  const toggle = (folder: DiffFolder) =>
    setFolds((m) => new Map(m).set(folder.path, !isOpen(folder)));
  const activate = (row: DiffRow) => {
    if (row.node.kind === "folder") toggle(row.node);
    else open(row.node.path);
  };
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const row = rows[focusAt];
    if (!row) return;
    const node = row.node;
    let to = focusAt;
    switch (event.key) {
      case "ArrowDown":
        to = Math.min(focusAt + 1, rows.length - 1);
        break;
      case "ArrowUp":
        to = Math.max(focusAt - 1, 0);
        break;
      case "Home":
        to = 0;
        break;
      case "End":
        to = rows.length - 1;
        break;
      case "ArrowRight":
        if (node.kind !== "folder") break;
        if (!isOpen(node)) toggle(node);
        else to = Math.min(focusAt + 1, rows.length - 1);
        break;
      case "ArrowLeft":
        if (node.kind === "folder" && isOpen(node)) toggle(node);
        else if (row.parent)
          to = rows.findIndex(
            (r) => r.node.kind === "folder" && r.node.path === row.parent,
          );
        break;
      case "Enter":
      case " ":
        activate(row);
        break;
      default:
        return;
    }
    // The keys stay in the tree: the page around it has its own.
    event.preventDefault();
    event.stopPropagation();
    if (to === focusAt || to < 0) return;
    setCursor(keyOf(rows[to]));
    const el = treeRef.current?.children[to];
    if (el instanceof HTMLElement) el.focus();
  };

  if (loading) {
    return (
      <div className="task-diff-view diff-state">
        <div className="diff-loading">{t("diff.loading")}</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="task-diff-view diff-state">
        <div className="diff-error">{t("diff.error", { error })}</div>
      </div>
    );
  }

  if (files.length === 0) {
    const empty = noWorktree
      ? t("diff.no_worktree")
      : uncommitted
        ? t("diff.worktree_clean")
        : t("diff.no_changes");
    return (
      <div className="task-diff-view diff-state">
        {drawn ? (
          <EmptyState art={noWorktree ? "noworktree" : "clean"}>
            {empty}
          </EmptyState>
        ) : (
          <div className="diff-empty">{empty}</div>
        )}
      </div>
    );
  }

  const added = files.reduce((n, f) => n + f.added, 0);
  const deleted = files.reduce((n, f) => n + f.deleted, 0);
  return (
    <div className="task-diff-view">
      <div className="diff-review">
        <div
          ref={treeRef}
          className="diff-tree"
          role="tree"
          aria-label={t("diff.tree")}
          onKeyDown={onKey}
        >
          {rows.map((row, i) => {
            const node = row.node;
            const folder = node.kind === "folder";
            return (
              <div
                key={keyOf(row)}
                role="treeitem"
                tabIndex={i === focusAt ? 0 : -1}
                aria-level={row.depth + 1}
                aria-expanded={folder ? isOpen(node) : undefined}
                aria-selected={i === showing}
                className={`diff-tree-row${folder ? " folder" : ""}${i === showing ? " current" : ""}`}
                style={{ paddingLeft: `${0.375 + row.depth * 0.875}rem` }}
                title={folder ? node.path : named(node.file)}
                onClick={() => {
                  setCursor(keyOf(row));
                  activate(row);
                }}
              >
                {folder ? (
                  <ChevronRight
                    size={12}
                    className="diff-tree-chevron"
                    aria-hidden="true"
                  />
                ) : (
                  <span className="diff-tree-chevron" />
                )}
                <span className="diff-tree-name">{node.name}</span>
                {folder ? (
                  <span className="diff-tree-files">
                    {t("diff.files", { count: node.files })}
                  </span>
                ) : (
                  <Flags file={node.file} />
                )}
                <Counts
                  added={folder ? node.added : node.file.added}
                  deleted={folder ? node.deleted : node.file.deleted}
                />
              </div>
            );
          })}
        </div>
        <div className="diff-main">
          <div className="diff-bar">
            <span>{t("diff.files", { count: files.length })}</span>
            <Counts added={added} deleted={deleted} />
            <div className="diff-mode-toggle">
              <button
                type="button"
                className={`diff-mode-btn ${viewMode === "unified" ? "active" : ""}`}
                aria-pressed={viewMode === "unified"}
                onClick={() => setViewMode("unified")}
              >
                {t("diff.unified")}
              </button>
              <button
                type="button"
                className={`diff-mode-btn ${viewMode === "split" ? "active" : ""}`}
                aria-pressed={viewMode === "split"}
                onClick={() => setViewMode("split")}
              >
                {t("diff.split")}
              </button>
            </div>
          </div>
          <ScrollRootContext value={column}>
            {/* It takes the focus: the arrows and the page keys scroll it. */}
            <div className="diff-files" ref={holdColumn} tabIndex={0}>
              {ordered.map((file, i) => (
                <FileDiff
                  key={`${uncommitted}-${reads}-${file.path}`}
                  task={task}
                  uncommitted={uncommitted}
                  file={file}
                  first={i === 0}
                  viewMode={viewMode}
                  comments={uncommitted ? NO_COMMENTS : localComments}
                  onCommentAdded={handleCommentAdded}
                />
              ))}
            </div>
          </ScrollRootContext>
        </div>
      </div>
    </div>
  );
});

// DiffView is what the review of a task shows: its committed work, or what its worktree holds not committed.
export type DiffView = "committed" | "uncommitted";

// TaskReview shows a task's diffs full size, beside the side panel, as a tilasm opens full size (full-size.ts): over
// the main shell, the view underneath kept as it was, its place and its scroll. A tab for the task's committed work,
// one for what its worktree holds not committed; refresh reads the diff again, a running worker changing it. The
// close button or Escape closes it, the focus back where the developer was; a click in the side panel closes it too,
// and goes where it leads.
export function TaskReview({
  task,
  view,
  shell,
  onView,
  onClose,
}: {
  task: Task;
  view: DiffView;
  // The main shell it opens over.
  shell: HTMLElement;
  onView: (view: DiffView) => void;
  // back: the focus goes back where it was; not after a click in the side panel, which takes it.
  onClose: (back: boolean) => void;
}) {
  const section = useRef<HTMLElement>(null);
  const enter = useRef<HTMLButtonElement>(null);
  const leave = useRef<HTMLButtonElement>(null);
  useFullSize(true, section, enter, leave, () => onClose(true));
  const closing = useRef(onClose);
  closing.current = onClose;
  useEffect(() => {
    const onClick = (event: MouseEvent) => {
      if (
        event.target instanceof Element &&
        event.target.closest("#djinn-sidebar")
      )
        closing.current(false);
    };
    document.addEventListener("click", onClick, true);
    return () => document.removeEventListener("click", onClick, true);
  }, []);
  // Each read of the diff is a new view: refresh counts them.
  const [reads, setReads] = useState(0);
  const title = t("diff.title", { task: task.code || task.title });
  const tabs: readonly [DiffView, string][] = [
    ["committed", t("diff.tab_committed")],
    ["uncommitted", t("diff.tab_uncommitted")],
  ];
  return createPortal(
    // A click on nothing that takes the focus leaves it in the review: the keys of the view underneath stay asleep.
    <section
      ref={section}
      className="full-over task-review"
      aria-label={title}
      tabIndex={-1}
    >
      <header className="task-review-head">
        <div className="task-review-title">
          <span className="eyebrow">{task.code}</span>
          <h2 title={task.title}>{task.title}</h2>
        </div>
        <div className="task-review-tabs" role="tablist" aria-label={title}>
          {tabs.map(([key, label]) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={view === key}
              className={`diff-mode-btn ${view === key ? "active" : ""}`}
              onClick={() => onView(key)}
            >
              {label}
            </button>
          ))}
        </div>
        <button
          type="button"
          className="icon-button"
          onClick={() => setReads((n) => n + 1)}
          aria-label={t("diff.refresh")}
          title={t("diff.refresh")}
        >
          <RefreshCw size={16} />
        </button>
        <button
          ref={leave}
          type="button"
          className="icon-button"
          onClick={() => onClose(true)}
          aria-label={t("diff.close")}
          aria-keyshortcuts="Escape"
          title={t("diff.close")}
        >
          <X size={18} />
        </button>
      </header>
      <div className="task-review-body" role="tabpanel">
        <TaskDiffView
          task={task}
          uncommitted={view === "uncommitted"}
          reads={reads}
          drawn
        />
      </div>
    </section>,
    shell,
  );
}

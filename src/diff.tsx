// TaskDiffView displays git diffs for a task: changed files list, then unified and side-by-side diff viewers.
import { memo, useEffect, useState } from "react";
import {
  DiffHunk,
  DiffLine,
  DiffLineKind,
  Task,
  TaskDiffFile,
  TaskFileDiff,
} from "../gen/ts/plan/v1/plan_pb";
import { useClients } from "./data/djinn";
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

export function UnifiedView({ hunks }: { hunks: readonly DiffHunk[] }) {
  return (
    <div className="diff-container">
      {hunks.map((hunk, hIdx) => (
        <div key={hIdx} className="diff-hunk">
          <div className="diff-hunk-header">{hunk.header}</div>
          <div className="diff-lines-unified">
            {hunk.lines.map((line, lIdx) => {
              const rowClass =
                line.kind === DiffLineKind.ADDED
                  ? "line-added"
                  : line.kind === DiffLineKind.DELETED
                    ? "line-deleted"
                    : "line-context";
              const marker =
                line.kind === DiffLineKind.ADDED
                  ? "+"
                  : line.kind === DiffLineKind.DELETED
                    ? "-"
                    : " ";
              return (
                <div key={lIdx} className={`diff-line-row ${rowClass}`}>
                  <span className="diff-cell-num">
                    {line.oldLine > 0 ? line.oldLine : ""}
                  </span>
                  <span className="diff-cell-num">
                    {line.newLine > 0 ? line.newLine : ""}
                  </span>
                  <span className="diff-cell-marker">{marker}</span>
                  <span className="diff-cell-code">
                    <LineTokens line={line} />
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}

export function SplitView({ hunks }: { hunks: readonly DiffHunk[] }) {
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
  taskId,
  file,
  viewMode,
  onToggleMode,
}: {
  taskId: string;
  file: TaskDiffFile;
  viewMode: "unified" | "split";
  onToggleMode: (mode: "unified" | "split") => void;
}) {
  const clients = useClients();
  const [fileDiff, setFileDiff] = useState<TaskFileDiff | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: taskId } },
        file: file.path,
      })
      .then((res) => {
        if (!active) return;
        setFileDiff(res.file ?? null);
        setLoading(false);
      })
      .catch((err) => {
        if (!active) return;
        setError(err.message || String(err));
        setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [clients, taskId, file.path]);

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
            <UnifiedView hunks={fileDiff?.hunks ?? []} />
          ) : (
            <SplitView hunks={fileDiff?.hunks ?? []} />
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
  const clients = useClients();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [files, setFiles] = useState<TaskDiffFile[]>([]);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<"unified" | "split">("unified");

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    clients.tasks
      .diff({
        task: { ref: { case: "id", value: task.id } },
        file: "",
      })
      .then((res) => {
        if (!active) return;
        setFiles(res.files);
        setLoading(false);
        if (res.files.length > 0) {
          const firstVisible = res.files.find((f) => !f.folded) ?? res.files[0];
          setSelectedPath(firstVisible.path);
        }
      })
      .catch((err) => {
        if (!active) return;
        setError(err.message || String(err));
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
          taskId={task.id}
          file={selectedFile}
          viewMode={viewMode}
          onToggleMode={setViewMode}
        />
      )}
    </div>
  );
});

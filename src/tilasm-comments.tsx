// The Comments column beside a tilasm's frame (L08, flow A): the developer's drafts on it, in the order written, the
// new one below. "On the whole tilasm" writes one; a draft is edited in place (Ctrl+Enter saves, Escape cancels) and
// deleted at once, with an Undo that stands five seconds instead of a dialog. A draft stays in Djinn's store and
// reaches no agent. Its model is src/data/comments.ts.
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { useEffect, useReducer, useRef, useState } from "react";

import type { TilasmComment } from "../gen/ts/plan/v1/tilasm_pb";
import {
  type AnchorKind,
  NEW,
  anchorKind,
  canSave,
  column,
  emptyColumn,
} from "./data/comments";
import { t } from "./i18n";

// What the column asks of the store; each resolves once the drafts shown are read again.
export interface CommentWrites {
  // Stores a draft's text on a tilasm: a new one on the whole of it without an identifier.
  put: (tilasmId: string, text: string, id?: string) => Promise<void>;
  // Deletes a draft; it resolves to the draft deleted, for its Undo.
  remove: (comment: TilasmComment) => Promise<TilasmComment>;
  // Puts back a draft deleted.
  putBack: (comment: TilasmComment) => Promise<void>;
}

const anchorText = (kind: AnchorKind) =>
  kind === "passage"
    ? t("tilasms.comment_anchor_passage")
    : kind === "element"
      ? t("tilasms.comment_anchor_element")
      : t("tilasms.comment_anchor_whole");

export function CommentsColumn({
  tilasmId,
  drafts,
  writes,
  onError,
  onClose,
}: {
  tilasmId: string;
  // The tilasm's drafts, in the order written.
  drafts: readonly TilasmComment[];
  writes: CommentWrites;
  onError: (error: unknown) => void;
  onClose: () => void;
}) {
  const [state, dispatch] = useReducer(column, emptyColumn);
  const [busy, setBusy] = useState(false);
  // The Undo goes once its time is past.
  const until = state.undo?.until;
  useEffect(() => {
    if (until === undefined) return;
    const timer = setTimeout(
      () => dispatch({ type: "tick", now: Date.now() }),
      Math.max(0, until - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [until]);
  const act = (write: () => Promise<void>) => {
    setBusy(true);
    write()
      .catch(onError)
      .finally(() => setBusy(false));
  };
  const save = () => {
    if (!canSave(state) || busy) return;
    const id = state.editing === NEW ? undefined : state.editing;
    act(async () => {
      await writes.put(tilasmId, state.text, id);
      dispatch({ type: "saved" });
    });
  };
  const editor = (label: string) => (
    <CommentEditor
      label={label}
      text={state.text}
      busy={busy}
      canSave={canSave(state)}
      onType={(text) => dispatch({ type: "type", text })}
      onSave={save}
      onCancel={() => dispatch({ type: "cancel" })}
    />
  );
  return (
    <aside className="tilasm-comments" aria-label={t("tilasms.comments")}>
      <div className="tilasm-comments-head">
        <strong>{t("tilasms.comments")}</strong>
        <button
          type="button"
          className="text-button"
          disabled={state.editing === NEW}
          onClick={() => dispatch({ type: "write" })}
        >
          <Plus size={13} aria-hidden="true" />
          {t("tilasms.comment_whole")}
        </button>
        <button
          type="button"
          className="icon-button"
          title={t("tilasms.comments_close")}
          aria-label={t("tilasms.comments_close")}
          onClick={onClose}
        >
          <X size={14} />
        </button>
      </div>
      <div className="tilasm-comments-body">
        {drafts.length === 0 && state.editing !== NEW && (
          <p className="muted-text">{t("tilasms.comments_none")}</p>
        )}
        <ol className="tilasm-comment-list">
          {drafts.map((comment) => (
            <li
              key={comment.id}
              id={`comment-${comment.id}`}
              className="tilasm-comment"
            >
              <div className="tilasm-comment-head">
                <span className="tilasm-code">{comment.code}</span>
                <span className="tilasm-comment-anchor">
                  {anchorText(anchorKind(comment))}
                </span>
                <span className="tilasm-comment-state">
                  {t("tilasms.comment_draft")}
                </span>
                {state.editing !== comment.id && (
                  <span className="tilasm-comment-actions">
                    <button
                      type="button"
                      className="icon-button"
                      title={t("tilasms.comment_edit", { code: comment.code })}
                      aria-label={t("tilasms.comment_edit", {
                        code: comment.code,
                      })}
                      onClick={() => dispatch({ type: "edit", comment })}
                    >
                      <Pencil size={13} />
                    </button>
                    <button
                      type="button"
                      className="icon-button"
                      disabled={busy}
                      title={t("tilasms.comment_delete", {
                        code: comment.code,
                      })}
                      aria-label={t("tilasms.comment_delete", {
                        code: comment.code,
                      })}
                      onClick={() =>
                        act(async () => {
                          const deleted = await writes.remove(comment);
                          dispatch({
                            type: "deleted",
                            comment: deleted,
                            now: Date.now(),
                          });
                        })
                      }
                    >
                      <Trash2 size={13} />
                    </button>
                  </span>
                )}
              </div>
              {state.editing === comment.id ? (
                editor(t("tilasms.comment_edit", { code: comment.code }))
              ) : (
                <p className="tilasm-comment-text">{comment.text}</p>
              )}
            </li>
          ))}
          {state.editing === NEW && (
            <li className="tilasm-comment">
              <div className="tilasm-comment-head">
                <span className="tilasm-comment-anchor">
                  {t("tilasms.comment_anchor_whole")}
                </span>
              </div>
              {editor(t("tilasms.comment_label"))}
            </li>
          )}
        </ol>
      </div>
      {state.undo && (
        <div className="tilasm-comments-undo" role="status">
          <span>
            {t("tilasms.comment_deleted", { code: state.undo.comment.code })}
          </span>
          <button
            type="button"
            className="text-button"
            disabled={busy}
            onClick={() => {
              const comment = state.undo?.comment;
              if (!comment) return;
              act(async () => {
                await writes.putBack(comment);
                dispatch({ type: "undone" });
              });
            }}
          >
            {t("tilasms.comment_undo")}
          </button>
        </div>
      )}
    </aside>
  );
}

// CommentEditor is a card being written: its text, focused, saved by Ctrl+Enter, given up by Escape.
function CommentEditor({
  label,
  text,
  busy,
  canSave,
  onType,
  onSave,
  onCancel,
}: {
  label: string;
  text: string;
  busy: boolean;
  canSave: boolean;
  onType: (text: string) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    el.focus({ preventScroll: true });
    el.setSelectionRange(el.value.length, el.value.length);
    // In sight within the column, which alone scrolls for it.
    const body = el.closest(".tilasm-comments-body");
    if (!body) return;
    const at = el.getBoundingClientRect();
    const view = body.getBoundingClientRect();
    if (at.bottom > view.bottom) body.scrollTop += at.bottom - view.bottom;
    else if (at.top < view.top) body.scrollTop -= view.top - at.top;
  }, []);
  return (
    <div className="tilasm-comment-editor">
      <textarea
        ref={area}
        value={text}
        rows={3}
        maxLength={4000}
        aria-label={label}
        aria-keyshortcuts="Control+Enter Escape"
        disabled={busy}
        onChange={(event) => onType(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
            event.preventDefault();
            onSave();
          } else if (event.key === "Escape") {
            // Escape gives up the card, and only the card: full size stays.
            event.preventDefault();
            onCancel();
          }
        }}
      />
      <div className="tilasm-comment-editor-foot">
        <span className="muted-text">{t("tilasms.comment_keys")}</span>
        <button
          type="button"
          className="button secondary small"
          disabled={busy}
          onClick={onCancel}
        >
          {t("tilasms.comment_cancel")}
        </button>
        <button
          type="button"
          className="button accent small"
          disabled={busy || !canSave}
          onClick={onSave}
        >
          {t("tilasms.comment_save")}
        </button>
      </div>
    </div>
  );
}

// DraftDot is the gold count of a tilasm's draft comments: they wait for the developer.
export function DraftDot({ count }: { count: number }) {
  const label = t("tilasms.drafts", { count });
  return (
    <span className="draft-dot" title={label} aria-label={label}>
      {count}
    </span>
  );
}

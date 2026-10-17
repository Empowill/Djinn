// The Comments column beside a tilasm's frame (L08, flow A): the developer's drafts on it, in the order written, the
// new one below. "On the whole tilasm" writes one, the Comment button over a passage selected in the frame another, on
// that passage (tilasm-passages.tsx), its quote on its card; a draft is edited in place (Ctrl+Enter saves, Escape cancels) and
// deleted at once, with an Undo that stands five seconds instead of a dialog. A draft stays in Djinn's store and
// reaches no agent until its foot sends them all (decision Q29): a last comment on the whole tilasm, then Launch or
// Iterate, confirmed in a line. A tilasm held, as the server computes it, greys its buttons, and its comments when a
// task works on it or its work was launched, with a line that says why. Its model is src/data/comments.ts.
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { useEffect, useReducer, useRef, useState } from "react";

import {
  SendChoice,
  type TilasmComment,
  type TilasmHold,
  TilasmHoldReason,
} from "../gen/ts/plan/v1/tilasm_pb";
import type { Passage } from "./data/bridge";
import {
  type AnchorKind,
  NEW,
  anchorKind,
  canSave,
  column,
  emptyColumn,
  sendFoot,
} from "./data/comments";
import { t } from "./i18n";

// What the column asks of the store; each resolves once the drafts shown are read again.
export interface CommentWrites {
  // Stores a draft's text on a tilasm: a new one without an identifier, on the passage given or the whole tilasm.
  put: (
    tilasmId: string,
    text: string,
    id?: string,
    passage?: Passage,
  ) => Promise<void>;
  // Deletes a draft; it resolves to the draft deleted, for its Undo.
  remove: (comment: TilasmComment) => Promise<TilasmComment>;
  // Puts back a draft deleted.
  putBack: (comment: TilasmComment) => Promise<void>;
  // Sends every draft of a tilasm, with text as one more on the whole of it, and the choice.
  send: (tilasmId: string, choice: SendChoice, text: string) => Promise<void>;
}

// holdLine says why a tilasm takes no send for now.
export function holdLine(hold: TilasmHold): string {
  switch (hold.reason) {
    case TilasmHoldReason.REWORK:
      return t("tilasms.hold_rework", { code: hold.code });
    case TilasmHoldReason.WORK:
      return t("tilasms.hold_work", { code: hold.code });
    case TilasmHoldReason.LAUNCHED:
      return hold.send > 0
        ? t("tilasms.hold_launched_send", { send: hold.send })
        : t("tilasms.hold_launched", { code: hold.code });
    case TilasmHoldReason.SENT:
      return t("tilasms.hold_sent", { send: hold.send });
    default:
      return "";
  }
}

const anchorText = (kind: AnchorKind, label = "") =>
  kind === "passage"
    ? label || t("tilasms.comment_anchor_passage")
    : kind === "element"
      ? label || t("tilasms.comment_anchor_element")
      : t("tilasms.comment_anchor_whole");

// Quote is the passage a card is on, as the developer selected it.
const Quote = ({ text }: { text: string }) => (
  <blockquote className="tilasm-comment-quote">{text}</blockquote>
);

export function CommentsColumn({
  tilasmId,
  code,
  version,
  drafts,
  hold,
  writes,
  onError,
  onClose,
  ask,
  onAsked = () => {},
  onWriting = () => {},
}: {
  tilasmId: string;
  // The tilasm's code and latest version, as the confirmation names them.
  code: string;
  version: number;
  // The tilasm's drafts, in the order written.
  drafts: readonly TilasmComment[];
  // Why it takes no send for now; none: it does.
  hold?: TilasmHold;
  writes: CommentWrites;
  onError: (error: unknown) => void;
  onClose: () => void;
  // A passage the developer asked to comment: a new card on it, then onAsked.
  ask?: Passage;
  onAsked?: () => void;
  // The passage of the new card being written, to highlight in the frame; undefined once there is none.
  onWriting?: (passage?: Passage) => void;
}) {
  const [state, dispatch] = useReducer(column, emptyColumn);
  const [busy, setBusy] = useState(false);
  const greyed = sendFoot(drafts.length, "", hold).greyed;
  const asked = useRef(onAsked);
  asked.current = onAsked;
  // A greyed column writes no comment: the passage asked for is let go.
  useEffect(() => {
    if (!ask) return;
    if (!greyed) dispatch({ type: "write", passage: ask });
    asked.current();
  }, [ask, greyed]);
  const writing = state.editing === NEW ? state.passage : undefined;
  const told = useRef(onWriting);
  told.current = onWriting;
  useEffect(() => told.current(writing), [writing]);
  useEffect(() => () => told.current(undefined), []);
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
      await writes.put(tilasmId, state.text, id, state.passage);
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
    <aside
      className={`tilasm-comments${greyed ? " held" : ""}`}
      aria-label={t("tilasms.comments")}
    >
      <div className="tilasm-comments-head">
        <strong>{t("tilasms.comments")}</strong>
        <button
          type="button"
          className="text-button"
          disabled={state.editing === NEW || greyed}
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
                  {anchorText(anchorKind(comment), comment.anchor?.label)}
                </span>
                <span className="tilasm-comment-state">
                  {t("tilasms.comment_draft")}
                </span>
                {state.editing !== comment.id && (
                  <span className="tilasm-comment-actions">
                    <button
                      type="button"
                      className="icon-button"
                      disabled={greyed}
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
                      disabled={busy || greyed}
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
              {comment.anchor?.exact && <Quote text={comment.anchor.exact} />}
              {state.editing === comment.id ? (
                editor(t("tilasms.comment_edit", { code: comment.code }))
              ) : (
                <p className="tilasm-comment-text">{comment.text}</p>
              )}
            </li>
          ))}
          {state.editing === NEW && (
            // A new passage asked for focuses the card again.
            <li
              key={`new-${state.passage?.start ?? ""}-${state.passage?.end ?? ""}`}
              className="tilasm-comment"
            >
              <div className="tilasm-comment-head">
                <span className="tilasm-comment-anchor">
                  {state.passage
                    ? anchorText("passage", state.passage.label)
                    : t("tilasms.comment_anchor_whole")}
                </span>
              </div>
              {state.passage && <Quote text={state.passage.exact} />}
              {editor(
                state.passage
                  ? t("tilasms.comment_label_passage")
                  : t("tilasms.comment_label"),
              )}
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
      <SendBar
        code={code}
        version={version}
        drafts={drafts.length}
        hold={hold}
        send={(choice, text) => writes.send(tilasmId, choice, text)}
        onError={onError}
      />
    </aside>
  );
}

// SendBar is the foot of the column: a last comment on the whole tilasm, then Launch or Iterate, which a line confirms
// (Enter sends, Escape gives up) before anything leaves for the Djinn.
function SendBar({
  code,
  version,
  drafts,
  hold,
  send,
  onError,
}: {
  code: string;
  version: number;
  drafts: number;
  hold?: TilasmHold;
  send: (choice: SendChoice, text: string) => Promise<void>;
  onError: (error: unknown) => void;
}) {
  const [text, setText] = useState("");
  // The button the confirmation stands for.
  const [confirming, setConfirming] = useState<SendChoice>();
  const [busy, setBusy] = useState(false);
  const foot = sendFoot(drafts, text, hold);
  const allowed =
    confirming === SendChoice.LAUNCH
      ? foot.launch
      : confirming === SendChoice.ITERATE && foot.iterate;
  const confirm = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (confirming !== undefined)
      confirm.current?.focus({ preventScroll: true });
  }, [confirming]);
  // A hold that comes meanwhile gives the confirmation up.
  if (confirming !== undefined && !allowed && !busy) setConfirming(undefined);
  const go = () => {
    if (confirming === undefined || busy) return;
    setBusy(true);
    send(confirming, text)
      .then(() => {
        setText("");
        setConfirming(undefined);
      })
      .catch(onError)
      .finally(() => setBusy(false));
  };
  const vars = { count: foot.count, code, version };
  const asked =
    confirming === SendChoice.ITERATE
      ? t("tilasms.send_confirm_iterate", vars)
      : foot.count > 0
        ? t("tilasms.send_confirm_launch", vars)
        : t("tilasms.send_confirm_launch_none", { code });
  return (
    <div
      className="tilasm-send"
      onKeyDown={(event) => {
        if (event.key !== "Escape" || confirming === undefined) return;
        // Escape gives up the confirmation, and only it: full size stays.
        event.preventDefault();
        setConfirming(undefined);
      }}
    >
      {hold && hold.reason !== TilasmHoldReason.UNSPECIFIED && (
        <p className="tilasm-send-hold">{holdLine(hold)}</p>
      )}
      <textarea
        value={text}
        rows={2}
        maxLength={4000}
        aria-label={t("tilasms.send_text")}
        placeholder={t("tilasms.send_placeholder")}
        disabled={busy || foot.greyed}
        onChange={(event) => setText(event.target.value)}
      />
      {confirming !== undefined ? (
        <div
          className="tilasm-send-confirm"
          role="group"
          aria-label={t("tilasms.send_confirm_label")}
        >
          <p>{asked}</p>
          <div className="tilasm-send-buttons">
            <button
              type="button"
              className="button secondary small"
              disabled={busy}
              onClick={() => setConfirming(undefined)}
            >
              {t("tilasms.send_not_yet")}
            </button>
            <button
              ref={confirm}
              type="button"
              className="button accent small"
              disabled={busy}
              onClick={go}
            >
              {t("tilasms.send_go")}
            </button>
          </div>
        </div>
      ) : (
        <div className="tilasm-send-buttons">
          <button
            type="button"
            className="button secondary small"
            title={t("tilasms.iterate_title")}
            disabled={!foot.iterate}
            onClick={() => setConfirming(SendChoice.ITERATE)}
          >
            {t("tilasms.iterate")}
          </button>
          <button
            type="button"
            className="button accent small"
            title={t("tilasms.launch_title")}
            disabled={!foot.launch}
            onClick={() => setConfirming(SendChoice.LAUNCH)}
          >
            {t("tilasms.launch")}
          </button>
        </div>
      )}
    </div>
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

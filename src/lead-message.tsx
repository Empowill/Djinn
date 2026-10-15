// A durable user indication, from a wish or its flight-plan card. Saving never starts a model.
// Enter saves, Shift+Enter breaks the line, Escape folds the pill and returns focus to its button.
import { Check, MessageSquare, Send, Terminal, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import {
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { Change, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { t } from "./i18n";
import { useInstructionDraft } from "./instruction-draft";
import { platformShortcut } from "./tooltip";
import { WorkspaceAction } from "./workspace-action";
import "./instruction.css";

export function LeadMessage({
  wish,
  attention,
  attentionCount = 0,
  attentionCollapsed = false,
  attentionRequest = 0,
  onAttentionOpen,
  onResume,
  onToast,
  variant = "floating",
}: {
  wish: Wish;
  attention?: ReactNode;
  attentionCount?: number;
  attentionCollapsed?: boolean;
  attentionRequest?: number;
  onAttentionOpen?: () => void;
  onResume: () => Promise<unknown>;
  onToast: (text: string) => void;
  variant?: "floating" | "card";
}) {
  const clients = useClients();
  const store = useStore();
  const { draft, state } = useInstructionDraft(store, wish.id);
  const [open, setOpen] = useState(false);
  const [resuming, setResuming] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);
  const id = useId();
  const mounted = useRef(true);
  const indicationShortcut = platformShortcut("I");
  const hasAttention = !!attention;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (open) {
      box.current?.focus({ preventScroll: true });
      return;
    }
    if (!restoreFocus.current) return;
    restoreFocus.current = false;
    const frame = window.requestAnimationFrame(() =>
      toggle.current?.focus({ preventScroll: true }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [open, wish.id]);
  useEffect(() => {
    if (open || !hasAttention || !attentionCollapsed) return;
    const frame = window.requestAnimationFrame(() =>
      toggle.current?.focus({ preventScroll: true }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [attentionCollapsed, hasAttention, open]);
  useLayoutEffect(() => {
    if (variant !== "floating" || !open) return;
    const textarea = box.current;
    if (!textarea) return;

    const resize = () => {
      textarea.style.height = "auto";
      textarea.style.overflowY = "hidden";
      const maxHeight = Number.parseFloat(
        window.getComputedStyle(textarea).maxHeight,
      );
      const height = Number.isFinite(maxHeight)
        ? Math.min(textarea.scrollHeight, maxHeight)
        : textarea.scrollHeight;
      textarea.style.height = `${height}px`;
      textarea.style.overflowY =
        textarea.scrollHeight > height ? "auto" : "hidden";
    };
    resize();
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, [open, state.text, variant]);
  useEffect(() => {
    if (variant !== "floating") return;
    const keys = (event: globalThis.KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        event.altKey ||
        event.shiftKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== "i"
      )
        return;
      if (document.querySelector(".modal-backdrop")) return;
      if (event.target instanceof HTMLElement) {
        if (
          event.target.isContentEditable ||
          event.target.matches(
            "input, textarea, select, [contenteditable='true']",
          ) ||
          event.target.closest(".xterm")
        )
          return;
      }
      event.preventDefault();
      setOpen(true);
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, [variant]);
  useEffect(() => {
    if (!state.notice) return;
    const notice = state.notice;
    const timer = setTimeout(() => {
      if (draft.get().notice === notice) draft.set({ notice: "" });
    }, 4000);
    return () => clearTimeout(timer);
  }, [draft, state.notice]);
  useEffect(() => {
    if (!attentionRequest || !hasAttention) return;
    setOpen(false);
  }, [attentionRequest, hasAttention]);

  const close = () => {
    restoreFocus.current = true;
    setOpen(false);
  };
  const send = async () => {
    // Synchronous lock across every mounted variant, including Enter followed immediately by a click.
    const request = draft.begin();
    if (!request) return;
    try {
      await clients.instructions.send({ wishId: wish.id, ...request });
      draft.set({ text: "", requestId: "", requestText: "", notice: "saved" });
      // A failed refresh cannot make a committed write look like a failed submission.
      void store.changed(wish.id, [Change.INSTRUCTION]);
    } catch (error) {
      const feedback = message(error);
      draft.set({ error: feedback });
      onToast(feedback);
    } finally {
      draft.set({ busy: false });
      if (mounted.current) box.current?.focus();
    }
  };
  const resume = async () => {
    if (resuming) return;
    setResuming(true);
    try {
      await onResume();
    } catch (error) {
      draft.set({ error: message(error) });
      onToast(message(error));
    } finally {
      if (mounted.current) {
        setResuming(false);
        box.current?.focus();
      }
    }
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      close();
    } else if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void send();
    }
  };
  if (variant === "floating") {
    return (
      <div className="lead-message lead-message-floating">
        <WorkspaceAction
          tone="secondary"
          expanded={open || (hasAttention && !attentionCollapsed)}
          // Keep the floating lead's wrapper stable while attention arrives or leaves. The creation submit owns the
          // shared layout identity; this wrapper keeps the editor draft mounted without joining that projection.
          sharedMotion={false}
          localMotion
          radius={hasAttention && !attentionCollapsed && !open ? 10 : 999}
          className={`workspace-action-lead${hasAttention && !open && !attentionCollapsed ? " workspace-action-dock-host" : ""}${hasAttention && !open && attentionCollapsed ? " workspace-action-dock-collapsed" : ""}`}
        >
          {hasAttention && (
            <div
              className={`workspace-action-attention-shell${open ? " is-editor-open" : ""}`}
            >
              <div
                className="workspace-action-attention-slot"
                hidden={open || attentionCollapsed}
                aria-hidden={open || attentionCollapsed}
              >
                {attention}
              </div>
              {!open && attentionCollapsed && (
                <div className="workspace-action-attention-stable">
                  <button
                    ref={toggle}
                    type="button"
                    className="workspace-action-control workspace-action-attention-label"
                    onClick={() => setOpen(true)}
                    title={t("lead_message.open")}
                    aria-label={t("lead_message.open")}
                    aria-expanded={false}
                  >
                    <MessageSquare size={15} aria-hidden="true" />
                    <span>{t("lead_message.open")}</span>
                  </button>
                  <button
                    type="button"
                    className="workspace-action-control workspace-action-attention-pending"
                    onClick={() => onAttentionOpen?.()}
                    title={t("lead_message.pending", { count: attentionCount })}
                    aria-label={t("lead_message.pending", {
                      count: attentionCount,
                    })}
                  >
                    <span className="live-dot" aria-hidden="true" />
                    <span aria-hidden="true">{attentionCount}</span>
                  </button>
                </div>
              )}
            </div>
          )}
          {open ? (
            <div
              id={`${id}-panel`}
              className="lead-message-panel workspace-action-editor-shell"
              role="dialog"
              aria-label={t("lead_message.title")}
              onKeyDown={(event) => {
                if (event.key !== "Escape") return;
                event.preventDefault();
                event.stopPropagation();
                close();
              }}
            >
              <form
                className={`workspace-action-editor ${wish.leadRunning ? "is-running" : ""}`}
                onSubmit={(event) => {
                  event.preventDefault();
                  void send();
                }}
              >
                {!wish.leadRunning && (
                  <button
                    type="button"
                    className="workspace-action-leading"
                    disabled={resuming}
                    onClick={() => void resume()}
                    title={t("lead_message.resume")}
                    aria-label={t("lead_message.resume")}
                  >
                    <Terminal size={13} aria-hidden="true" />
                  </button>
                )}
                <label
                  htmlFor={`${id}-input`}
                  className="workspace-action-sr-only"
                >
                  {t("lead_message.placeholder")}
                </label>
                <textarea
                  id={`${id}-input`}
                  ref={box}
                  rows={1}
                  value={state.text}
                  onChange={(event) =>
                    draft.set({
                      text: event.target.value,
                      error: "",
                      notice: "",
                    })
                  }
                  onKeyDown={keys}
                  placeholder={t("lead_message.placeholder")}
                  aria-label={t("lead_message.placeholder")}
                  aria-invalid={!!state.error}
                  aria-describedby={
                    state.error
                      ? `${id}-error`
                      : state.notice
                        ? `${id}-notice`
                        : undefined
                  }
                  readOnly={state.busy}
                />
                {state.error && (
                  <p
                    id={`${id}-error`}
                    className="instruction-error workspace-action-feedback"
                    role="alert"
                  >
                    {state.error}
                  </p>
                )}
                {state.notice && (
                  <span
                    id={`${id}-notice`}
                    className="lead-message-sent workspace-action-feedback"
                    role="status"
                  >
                    <Check size={12} aria-hidden="true" />
                    {t("instruction.saved")}
                  </span>
                )}
                <button
                  type="submit"
                  className="workspace-action-send"
                  disabled={state.busy || !state.text.trim()}
                  title={t("lead_message.send")}
                  aria-label={t("lead_message.send")}
                >
                  <Send size={14} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="workspace-action-close"
                  onClick={close}
                  title={t("lead_message.close")}
                  aria-label={t("lead_message.close")}
                >
                  <X size={13} aria-hidden="true" />
                </button>
              </form>
            </div>
          ) : !hasAttention ? (
            <button
              ref={toggle}
              type="button"
              className="lead-message-toggle workspace-action-control"
              onClick={() => setOpen(true)}
              title={`${t("lead_message.open")} · ${indicationShortcut.label}`}
              aria-label={t("lead_message.open")}
              aria-keyshortcuts={indicationShortcut.aria}
              aria-expanded={false}
              aria-controls={`${id}-panel`}
            >
              <MessageSquare size={15} aria-hidden="true" />
              <span>{t("lead_message.open")}</span>
            </button>
          ) : null}
        </WorkspaceAction>
      </div>
    );
  }
  return (
    <div
      className={`lead-message lead-message-${variant}${variant === "card" ? " lead-message-card" : ""}`}
    >
      <AnimatePresence>
        {open && (
          <motion.div
            id={`${id}-panel`}
            className="lead-message-panel"
            role="dialog"
            aria-label={t("lead_message.title")}
            initial={{ opacity: 0, y: 8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 6 }}
            onKeyDown={(event) => {
              if (event.key !== "Escape") return;
              event.preventDefault();
              event.stopPropagation();
              close();
            }}
          >
            <header>
              <span>{t("lead_message.title")}</span>
              <button
                className="icon-button"
                onClick={close}
                title={t("lead_message.close")}
                aria-label={t("lead_message.close")}
              >
                <X size={13} />
              </button>
            </header>
            {!wish.leadRunning && (
              <div className="lead-message-off">
                <p>{t("lead_message.not_running")}</p>
                <button
                  className="button secondary small"
                  disabled={resuming}
                  onClick={() => void resume()}
                >
                  <Terminal size={13} />
                  <span>{t("lead_message.resume")}</span>
                </button>
              </div>
            )}
            <textarea
              ref={box}
              rows={3}
              value={state.text}
              onChange={(event) =>
                draft.set({ text: event.target.value, error: "", notice: "" })
              }
              onKeyDown={keys}
              placeholder={t("lead_message.placeholder")}
              aria-label={t("lead_message.placeholder")}
              aria-invalid={!!state.error}
              aria-describedby={state.error ? `${id}-error` : `${id}-hint`}
              readOnly={state.busy}
            />
            {state.error && (
              <p id={`${id}-error`} className="instruction-error" role="alert">
                {state.error}
              </p>
            )}
            <footer>
              <span
                id={`${id}-hint`}
                className="lead-message-hint"
                role="status"
              >
                {state.notice ? (
                  <span className="lead-message-sent">
                    <Check size={12} aria-hidden="true" />
                    {t("instruction.saved")}
                  </span>
                ) : (
                  t("lead_message.hint")
                )}
              </span>
              <button
                className="icon-button"
                onClick={() => void send()}
                disabled={state.busy || !state.text.trim()}
                title={t("lead_message.send")}
                aria-label={t("lead_message.send")}
              >
                <Send size={14} />
              </button>
            </footer>
          </motion.div>
        )}
      </AnimatePresence>
      <button
        ref={toggle}
        className={`lead-message-toggle ${wish.leadRunning ? "" : "off"} ${open ? "open" : ""}`}
        onClick={() => (open ? close() : setOpen(true))}
        title={t("lead_message.open")}
        aria-label={
          variant === "card"
            ? t("instruction.open_for", { wish: wish.title })
            : t("lead_message.open")
        }
        aria-expanded={open}
        aria-controls={open ? `${id}-panel` : undefined}
      >
        <MessageSquare size={variant === "card" ? 13 : 16} />
      </button>
    </div>
  );
}

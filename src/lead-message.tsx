// A durable user indication, from a wish or its flight-plan card. Saving never starts a model.
// Enter saves, Shift+Enter breaks the line, Escape folds the box and returns focus to its button.
import { Check, MessageSquare, Send, Terminal, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react";

import { Change, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { t } from "./i18n";
import { useInstructionDraft } from "./instruction-draft";
import "./instruction.css";

export function LeadMessage({
  wish,
  onResume,
  onToast,
  variant = "floating",
}: {
  wish: Wish;
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
  const id = useId();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (open) box.current?.focus();
  }, [open, wish.id]);
  useEffect(() => {
    if (!state.notice) return;
    const notice = state.notice;
    const timer = setTimeout(() => {
      if (draft.get().notice === notice) draft.set({ notice: "" });
    }, 4000);
    return () => clearTimeout(timer);
  }, [draft, state.notice]);

  const close = () => {
    setOpen(false);
    toggle.current?.focus();
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
  return (
    <div className={`lead-message lead-message-${variant}`}>
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

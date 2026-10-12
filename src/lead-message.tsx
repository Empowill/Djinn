// Writing to the lead from a wish: the pill at the bottom centre of the wish unfolds into a box whose text goes to the
// one lead's terminal (LeadService.Tell), about this wish, as if the developer typed it there. Enter sends, Shift+Enter
// breaks the line, Escape folds the pill and returns focus to it, Cmd/Ctrl+I opens it. The draft stays with the wish.
import { Check, Clock, MessageSquare, Send, Terminal, X } from "lucide-react";
import {
  type KeyboardEvent,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { type Wish } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { t } from "./i18n";
import { useInstructionDraft } from "./instruction-draft";
import { platformShortcut, TooltipButton } from "./tooltip";
import { WorkspaceAction } from "./workspace-action";
import "./instruction.css";

export function LeadMessage({
  wish,
  stopped,
  onResume,
}: {
  wish: Wish;
  // No lead, or it ended or could not start: the pill offers to start it, as Lead does, before its text can reach it.
  stopped: boolean;
  onResume: () => Promise<unknown>;
}) {
  const clients = useClients();
  const store = useStore();
  const { draft, state } = useInstructionDraft(store, wish.id);
  const [open, setOpen] = useState(false);
  const [resuming, setResuming] = useState(false);
  // The last text was refused, the lead not running in its terminal: the pill offers to start it too.
  const [refused, setRefused] = useState(false);
  const offline = stopped || refused;
  const box = useRef<HTMLTextAreaElement>(null);
  const restoreFocus = useRef(false);
  const id = useId();
  const mounted = useRef(true);
  const shortcut = platformShortcut("I");
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
      document.getElementById(`${id}-toggle`)?.focus({ preventScroll: true }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [open, wish.id, id]);
  useLayoutEffect(() => {
    if (!open) return;
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
  }, [open, state.text]);
  useEffect(() => {
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
  }, []);
  useEffect(() => {
    if (!state.notice) return;
    const notice = state.notice;
    const timer = setTimeout(() => {
      if (draft.get().notice === notice) draft.set({ notice: "" });
    }, 4000);
    return () => clearTimeout(timer);
  }, [draft, state.notice]);

  const close = () => {
    restoreFocus.current = true;
    setOpen(false);
  };
  const send = async () => {
    // Synchronous lock, so Enter followed at once by a click sends the text once.
    const text = draft.begin();
    if (!text) return;
    try {
      const { waiting } = await clients.lead.tell({ text, wishId: wish.id });
      draft.set({ text: "", notice: waiting ? "waiting" : "sent" });
      setRefused(false);
    } catch (error) {
      // Said in the pill, by the text it concerns: no toast besides.
      draft.set({ error: message(error) });
      setRefused(true);
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
      draft.set({ error: "" });
      setRefused(false);
    } catch {
      // Lead's own toast says why it could not start, as from the header's Lead.
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
    <div className="lead-message">
      <WorkspaceAction
        tone="secondary"
        expanded={open}
        className="workspace-action-lead"
      >
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
              className={`workspace-action-editor ${offline ? "" : "is-running"}`}
              onSubmit={(event) => {
                event.preventDefault();
                void send();
              }}
            >
              {offline && (
                <TooltipButton
                  className="workspace-action-leading"
                  disabled={resuming}
                  onClick={() => void resume()}
                  label={t("lead_message.resume")}
                >
                  <Terminal size={13} aria-hidden="true" />
                </TooltipButton>
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
                  className={`lead-message-sent workspace-action-feedback ${state.notice}`}
                  role="status"
                >
                  {state.notice === "waiting" ? (
                    <Clock size={12} aria-hidden="true" />
                  ) : (
                    <Check size={12} aria-hidden="true" />
                  )}
                  {t(`lead_message.${state.notice}`)}
                </span>
              )}
              <TooltipButton
                type="submit"
                className="workspace-action-send"
                disabled={state.busy || !state.text.trim()}
                label={t("lead_message.send")}
                tooltip={t("lead_message.hint")}
              >
                <Send size={14} aria-hidden="true" />
              </TooltipButton>
              <TooltipButton
                className="workspace-action-close"
                onClick={close}
                label={t("lead_message.close")}
              >
                <X size={13} aria-hidden="true" />
              </TooltipButton>
            </form>
          </div>
        ) : (
          <TooltipButton
            id={`${id}-toggle`}
            className="workspace-action-control"
            onClick={() => setOpen(true)}
            label={t("lead_message.open")}
            shortcut={shortcut.label}
            aria-keyshortcuts={shortcut.aria}
            aria-expanded={false}
            aria-controls={`${id}-panel`}
          >
            <MessageSquare size={15} aria-hidden="true" />
            <span>{t("lead_message.open")}</span>
          </TooltipButton>
        )}
      </WorkspaceAction>
    </div>
  );
}

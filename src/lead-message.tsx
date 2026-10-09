// A discreet button at the bottom right of a wish: a click unfolds a box whose text goes to the wish's lead, as if
// typed in its terminal (WishService.Tell). Enter sends, Shift+Enter breaks the line, Escape folds it. Without a lead
// running, the box says so and offers to resume it. A text that waits in the terminal says why, until the next one.
import {
  Check,
  Hourglass,
  MessageSquare,
  Send,
  Terminal,
  X,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type KeyboardEvent, useEffect, useRef, useState } from "react";

import { TellWait, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients } from "./data/djinn";
import { t } from "./i18n";

export function LeadMessage({
  wish,
  onResume,
  onToast,
}: {
  wish: Wish;
  onResume: () => Promise<unknown>;
  onToast: (text: string) => void;
}) {
  const clients = useClients();
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<"" | "sent" | "typing" | "choice">("");
  const box = useRef<HTMLTextAreaElement>(null);
  const running = wish.leadRunning;

  useEffect(() => {
    if (open && running) box.current?.focus();
  }, [open, running]);
  // The confirmation fades on its own; why a text waits stays.
  useEffect(() => {
    if (sent !== "sent") return;
    const timer = setTimeout(() => setSent(""), 4000);
    return () => clearTimeout(timer);
  }, [sent]);

  const send = async () => {
    const said = text.trim();
    if (!said || busy || !running) return;
    setBusy(true);
    try {
      const res = await clients.wishes.tell({ wishId: wish.id, text: said });
      setText("");
      setSent(
        res.wait === TellWait.CHOICE
          ? "choice"
          : res.waiting
            ? "typing"
            : "sent",
      );
    } catch (error) {
      onToast(message(error)); // The text stays, to send again.
    } finally {
      setBusy(false);
      box.current?.focus();
    }
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
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
      <AnimatePresence>
        {open && (
          <motion.div
            className="lead-message-panel"
            role="dialog"
            aria-label={t("lead_message.title")}
            initial={{ opacity: 0, y: 8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 6 }}
            onKeyDown={(event) => {
              if (event.key !== "Escape") return;
              event.stopPropagation();
              setOpen(false);
            }}
          >
            <header>
              <span>{t("lead_message.title")}</span>
              <button
                className="icon-button"
                onClick={() => setOpen(false)}
                title={t("lead_message.close")}
                aria-label={t("lead_message.close")}
              >
                <X size={13} />
              </button>
            </header>
            {!running && (
              <div className="lead-message-off">
                <p>{t("lead_message.not_running")}</p>
                <button
                  className="button secondary small"
                  onClick={() => void onResume().catch(() => undefined)}
                >
                  <Terminal size={13} />
                  <span>{t("lead_message.resume")}</span>
                </button>
              </div>
            )}
            <textarea
              ref={box}
              rows={3}
              value={text}
              onChange={(event) => setText(event.target.value)}
              onKeyDown={keys}
              placeholder={t("lead_message.placeholder")}
              aria-label={t("lead_message.placeholder")}
              disabled={!running}
            />
            <footer>
              <span className="lead-message-hint" aria-live="polite">
                {sent === "sent" ? (
                  <span className="lead-message-sent">
                    <Check size={12} aria-hidden="true" />
                    {t("lead_message.sent")}
                  </span>
                ) : sent ? (
                  <span className="lead-message-sent waiting">
                    <Hourglass size={12} aria-hidden="true" />
                    {sent === "choice"
                      ? t("lead_message.waiting_choice")
                      : t("lead_message.waiting_typing")}
                  </span>
                ) : (
                  t("lead_message.hint")
                )}
              </span>
              <button
                className="icon-button"
                onClick={() => void send()}
                disabled={!running || busy || !text.trim()}
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
        className={`lead-message-toggle ${running ? "" : "off"} ${open ? "open" : ""}`}
        onClick={() => setOpen(!open)}
        title={running ? t("lead_message.open") : t("lead_message.not_running")}
        aria-label={t("lead_message.open")}
        aria-expanded={open}
      >
        <MessageSquare size={16} />
      </button>
    </div>
  );
}

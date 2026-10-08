// The frame of the window, kept from the first interface: the brand, a modal, a toast.
import { AnimatePresence, motion } from "motion/react";
import { X } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";

import { t } from "./i18n";

export function Brand({ small = false }: { small?: boolean }) {
  return (
    <span className={`brand ${small ? "small" : ""}`}>
      <span className="brand-mark">
        <svg viewBox="0 0 30 30" fill="none">
          <path
            d="M15 2 28 9.5v11L15 28 2 20.5v-11Z"
            stroke="currentColor"
            strokeWidth="1.2"
          />
          <path
            d="m8 11 7-4 7 4v8l-7 4-7-4Zm7-4v16M8 11l14 8m0-8L8 19"
            stroke="currentColor"
            strokeWidth="1.2"
          />
        </svg>
      </span>
      {!small && (
        <>
          djinn<span className="brand-dot">.</span>
        </>
      )}
    </span>
  );
}

export function ModalFrame({
  title,
  eyebrow,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  eyebrow: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const previousFocus = useRef<HTMLElement | null>(
    typeof document === "undefined"
      ? null
      : (document.activeElement as HTMLElement | null),
  );
  useEffect(() => {
    const el = ref.current;
    const focusable = () =>
      Array.from(
        el?.querySelectorAll<HTMLElement>(
          'button,input,textarea,select,[tabindex="0"]',
        ) || [],
      ).filter(
        (node) =>
          !node.matches(":disabled") && node.getClientRects().length > 0,
      );
    focusable()?.[0]?.focus({ preventScroll: true });
    const handle = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCloseRef.current();
      if (e.key === "Tab") {
        const nodes = focusable();
        if (!nodes?.length) return;
        const first = nodes[0],
          last = nodes[nodes.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", handle);
    return () => {
      document.removeEventListener("keydown", handle);
      if (previousFocus.current?.isConnected)
        previousFocus.current.focus({ preventScroll: true });
    };
  }, []);
  return (
    <motion.div
      className="modal-backdrop"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <motion.div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`modal ${wide ? "wide" : ""}`}
        initial={{ opacity: 0, y: 24, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 15, scale: 0.98 }}
        transition={{ duration: 0.22 }}
      >
        <div className="modal-top">
          <span className="eyebrow">{eyebrow}</span>
          <button
            className="icon-button"
            onClick={onClose}
            aria-label={t("app.close_window")}
          >
            <X size={18} />
          </button>
        </div>
        <h2>{title}</h2>
        {children}
      </motion.div>
    </motion.div>
  );
}

// Toast says one thing for a moment, at the bottom of the window.
export function Toast({
  text,
  onClose,
}: {
  text: string;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!text) return;
    const timer = setTimeout(onClose, 6000);
    return () => clearTimeout(timer);
  }, [text, onClose]);
  return (
    <AnimatePresence>
      {text && (
        <motion.div
          role="status"
          className="toast"
          initial={{ opacity: 0, y: 20, scale: 0.96 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: 10 }}
        >
          <span className="toast-symbol">✳</span>
          {text}
          <button onClick={onClose} aria-label={t("app.close_notification")}>
            <X size={14} />
          </button>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

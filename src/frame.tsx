// The frame of the window, kept from the first interface: the brand, a modal, a toast.
import { AnimatePresence, motion } from "motion/react";
import { X } from "lucide-react";
import { type ReactNode, type RefObject, useEffect, useRef } from "react";

import icon from "../build/icon.svg";
import { t } from "./i18n";
import { Wordmark } from "./wordmark";

// The mark is the app's icon, the one drawing the dock and the window show (build/icon.svg),
// paired with its high-contrast light-ground twin (build/icon-light.svg).
export function Brand({ small = false }: { small?: boolean }) {
  return (
    <span className={`brand ${small ? "small" : ""}`}>
      <span className="brand-mark">
        <img className="brand-mark-dark" src={icon} alt="" />
        <svg
          className="brand-mark-light"
          viewBox="0 0 512 512"
          aria-hidden="true"
        >
          <g transform="translate(256 256) scale(3.4) translate(-74 -74)">
            <circle
              cx="74"
              cy="74"
              r="70"
              fill="#f4f8f6"
              stroke="#146b5a"
              strokeWidth="3.8"
            />
            <path
              d="M138 74L129.5 74M119.25 119.25L113.24 113.24M74 138L74 129.5M28.75 119.25L34.76 113.24M10 74L18.5 74M28.75 28.75L34.76 34.76M74 10L74 18.5M119.25 28.75L113.24 34.76"
              stroke="#0f594a"
              strokeWidth="3"
              strokeLinecap="round"
            />
            <path
              d="M137.45 82.35L133.98 81.9M135.82 90.56L132.44 89.66M133.13 98.49L129.89 97.15M129.43 106L126.39 104.25M124.77 112.96L122 110.83M112.96 124.77L110.83 122M106 129.43L104.25 126.39M98.49 133.13L97.15 129.89M90.56 135.82L89.66 132.44M82.35 137.45L81.9 133.98M65.65 137.45L66.1 133.98M57.44 135.82L58.34 132.44M49.51 133.13L50.85 129.89M42 129.43L43.75 126.39M35.04 124.77L37.17 122M23.23 112.96L26 110.83M18.57 106L21.61 104.25M14.87 98.49L18.11 97.15M12.18 90.56L15.56 89.66M10.55 82.35L14.02 81.9M10.55 65.65L14.02 66.1M12.18 57.44L15.56 58.34M14.87 49.51L18.11 50.85M18.57 42L21.61 43.75M23.23 35.04L26 37.17M35.04 23.23L37.17 26M42 18.57L43.75 21.61M49.51 14.87L50.85 18.11M57.44 12.18L58.34 15.56M65.65 10.55L66.1 14.02M82.35 10.55L81.9 14.02M90.56 12.18L89.66 15.56M98.49 14.87L97.15 18.11M106 18.57L104.25 21.61M112.96 23.23L110.83 26M124.77 35.04L122 37.17M129.43 42L126.39 43.75M133.13 49.51L129.89 50.85M135.82 57.44L132.44 58.34M137.45 65.65L133.98 66.1"
              stroke="#146b5a"
              strokeWidth="1.8"
              strokeLinecap="round"
            />
            <rect
              x="40"
              y="40"
              width="68"
              height="68"
              fill="#e5f2ed"
              fillOpacity="0.7"
              stroke="#0f594a"
              strokeWidth="4.2"
              strokeLinejoin="round"
            />
            <rect
              x="40"
              y="40"
              width="68"
              height="68"
              transform="rotate(45 74 74)"
              fill="#e5f2ed"
              fillOpacity="0.7"
              stroke="#0f594a"
              strokeWidth="4.2"
              strokeLinejoin="round"
            />
            <circle
              cx="74"
              cy="74"
              r="22"
              fill="#d6e9e3"
              stroke="#0f594a"
              strokeWidth="3.6"
            />
            <path
              d="M74 55 C 85 68, 83 84, 74 87 C 65 84, 63 68, 74 55 Z"
              fill="#c88214"
              stroke="#7a4b00"
              strokeWidth="1.5"
              strokeLinejoin="round"
            />
            <path
              d="M74 62 C 79 70, 78 80, 74 82 C 70 80, 69 70, 74 62 Z"
              fill="#f0b24a"
            />
          </g>
        </svg>
      </span>
      {!small && <Wordmark />}
    </span>
  );
}

// useDialogKeys keeps a dialog's keys: focus on its first control, Tab cycling within it, Escape closing it, and the
// focus given back to what had it once the dialog closes.
export function useDialogKeys(
  ref: RefObject<HTMLElement | null>,
  onClose: () => void,
) {
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const previousFocus = useRef<HTMLElement | null>(
    typeof document === "undefined"
      ? null
      : (document.activeElement as HTMLElement | null),
  );
  useEffect(() => {
    const el = ref.current;
    const previous = previousFocus.current;
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
      // A field that takes Escape for itself (a comment box cancelling) keeps the dialog open.
      if (e.key === "Escape" && !e.defaultPrevented) onCloseRef.current();
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
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    };
  }, [ref]);
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
  useDialogKeys(ref, onClose);
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

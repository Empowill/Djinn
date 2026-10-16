import { memo, useEffect, useState } from "react";
import { motion } from "motion/react";
import { X } from "lucide-react";

import { language, t } from "./i18n";
import { shownTheme } from "./theme";

// DocsPath is where djinn serves its documentation, the Grimoire (docs/site): a splash page, then the book, Getting
// started, a page per concept, and the command line.
export const DocsPath = "/docs/";

// Docs shows the Grimoire over the window, in the window's theme, its navigation in the window's language. It is a page
// of djinn's own server, in a frame: the window keeps its wish, its terminal and its streams behind it.
export const Docs = memo(function Docs({ onClose }: { onClose: () => void }) {
  // The frame's address is set once: a new one would reload the site, its place and its search lost.
  const [src] = useState(
    () => `${DocsPath}?theme=${shownTheme()}&lang=${language}&embedded=1`,
  );
  useEffect(() => {
    const handle = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handle);
    return () => document.removeEventListener("keydown", handle);
  }, [onClose]);
  return (
    <motion.div
      className="modal-backdrop docs-backdrop"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <motion.div
        role="dialog"
        aria-modal="true"
        aria-label={t("docs.title")}
        className="docs-panel"
        initial={{ opacity: 0, y: 24, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 15, scale: 0.98 }}
        transition={{ duration: 0.22 }}
      >
        <button
          className="icon-button docs-close"
          onClick={onClose}
          aria-label={t("app.close_window")}
          autoFocus
        >
          <X size={18} />
        </button>
        <iframe title={t("docs.title")} src={src} />
      </motion.div>
    </motion.div>
  );
});

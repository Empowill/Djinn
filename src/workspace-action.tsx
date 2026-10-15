import { motion, useReducedMotion } from "motion/react";
import { type ReactNode } from "react";

export type WorkspaceActionTone = "primary" | "secondary";

const ACTION_LAYOUT_ID = "workspace-action";
const ACTION_TONE_CLASS: Record<WorkspaceActionTone, string> = {
  primary: "workspace-action-primary",
  secondary: "workspace-action-secondary",
};

// WorkspaceAction is the shared surface used by wish creation and the floating indication editor. The content changes
// between those states, while the creation/editor pill keeps one motion identity so its geometry travels with the
// action. Attention docks can retain this wrapper for draft state while opting out of that shared projection. The
// content uses a position-only projection so Motion never applies a size scale to its text or controls.
export function WorkspaceAction({
  children,
  tone,
  expanded = false,
  className = "",
  layoutId = ACTION_LAYOUT_ID,
  dataWishSection,
  radius = 999,
  sharedMotion = true,
  localMotion = true,
}: {
  children: ReactNode;
  tone: WorkspaceActionTone;
  expanded?: boolean;
  className?: string;
  layoutId?: string;
  dataWishSection?: string;
  radius?: number;
  /** Keep a stateful action out of the creation pill's shared layout projection. */
  sharedMotion?: boolean;
  /** Keep local open/close geometry and content projection when shared motion is disabled. */
  localMotion?: boolean;
}) {
  const reducedMotion = useReducedMotion();
  const transition = reducedMotion
    ? { duration: 0 }
    : {
        type: "spring" as const,
        stiffness: 320,
        damping: 28,
        mass: 0.8,
      };
  return (
    <motion.div
      layout={localMotion}
      layoutId={sharedMotion ? `${layoutId}-surface` : undefined}
      layoutAnchor={{ x: 0.5, y: 1 }}
      className={`workspace-action ${ACTION_TONE_CLASS[tone]}${expanded ? " is-expanded" : ""} ${className}`}
      data-wish-section={dataWishSection}
      initial={false}
      transition={localMotion ? transition : { duration: 0 }}
      style={{ borderRadius: radius }}
    >
      <div className="workspace-action-surface" aria-hidden="true">
        {sharedMotion && (
          <motion.div
            className="workspace-action-surface-fill"
            // The outer pill owns the shared geometry. Keep the color fill local so both tones
            // enter from the bottom instead of letting a shared layout projection reverse this
            // direction when returning from an indication to wish creation.
            initial={
              reducedMotion ? false : { y: tone === "primary" ? "101%" : "0%" }
            }
            animate={{ y: tone === "primary" ? "0%" : "-101%" }}
            transition={transition}
            aria-hidden="true"
          />
        )}
      </div>
      <motion.div
        layout={localMotion ? "position" : false}
        transition={localMotion ? transition : { duration: 0 }}
        className="workspace-action-content"
      >
        {children}
      </motion.div>
    </motion.div>
  );
}

import { motion, useReducedMotion } from "motion/react";
import { type ReactNode } from "react";

export type WorkspaceActionTone = "primary" | "secondary";

const ACTION_LAYOUT_ID = "workspace-action";
const ACTION_TONE_CLASS: Record<WorkspaceActionTone, string> = {
  primary: "workspace-action-primary",
  secondary: "workspace-action-secondary",
};

// WorkspaceAction is the shared surface used by wish creation and the floating indication editor. The content changes
// between those states, while the pill keeps one motion identity so its geometry travels with the action. The content
// uses a position-only projection so Motion never applies a size scale to its text or controls.
export function WorkspaceAction({
  children,
  tone,
  expanded = false,
  className = "",
  layoutId = ACTION_LAYOUT_ID,
}: {
  children: ReactNode;
  tone: WorkspaceActionTone;
  expanded?: boolean;
  className?: string;
  layoutId?: string;
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
      layout
      layoutId={`${layoutId}-surface`}
      layoutAnchor={{ x: 0.5, y: 1 }}
      className={`workspace-action ${ACTION_TONE_CLASS[tone]}${expanded ? " is-expanded" : ""} ${className}`}
      initial={false}
      transition={transition}
      style={{ borderRadius: 999 }}
    >
      <div className="workspace-action-surface" aria-hidden="true">
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
      </div>
      <motion.div
        layout="position"
        transition={transition}
        className="workspace-action-content"
      >
        {children}
      </motion.div>
    </motion.div>
  );
}

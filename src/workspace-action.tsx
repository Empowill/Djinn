import { motion, useReducedMotion } from "motion/react";
import { type ReactNode } from "react";

export type WorkspaceActionTone = "primary" | "secondary";

const ACTION_LAYOUT_ID = "workspace-action";
const ACTION_TONE_CLASS: Record<WorkspaceActionTone, string> = {
  primary: "workspace-action-primary",
  secondary: "workspace-action-secondary",
};

// WorkspaceAction is the shared surface used by wish creation and the floating indication editor. The content changes
// between those states, while the surface keeps one motion identity so the background travels with the action.
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
        layout: {
          duration: 0.34,
          ease: [0.22, 1, 0.36, 1],
        },
        backgroundColor: { duration: 0.26 },
      };
  return (
    <motion.div
      layout
      className={`workspace-action ${ACTION_TONE_CLASS[tone]}${expanded ? " is-expanded" : ""} ${className}`}
      transition={transition}
    >
      <motion.div
        layoutId={`${layoutId}-surface`}
        className="workspace-action-surface"
        transition={transition}
        aria-hidden="true"
      >
        <motion.div
          layoutId={`${layoutId}-fill`}
          className="workspace-action-surface-fill"
          initial={false}
          animate={{ x: tone === "primary" ? "0%" : "101%" }}
          transition={transition}
          aria-hidden="true"
        />
      </motion.div>
      <div className="workspace-action-content">{children}</div>
    </motion.div>
  );
}

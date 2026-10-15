import {
  CheckCircle2,
  CirclePause,
  CirclePlay,
  CircleStop,
  X,
} from "lucide-react";
import { createPortal } from "react-dom";
import {
  type MouseEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";

import type { Project, Task } from "../gen/ts/plan/v1/plan_pb";
import type { Tone } from "./data/format";
import { t } from "./i18n";
import { StatusBadge } from "./status";

import "./task-drawer.css";

export function TaskDrawer({
  id,
  open,
  task,
  project,
  tone,
  statusLabel,
  body,
  summary,
  doneForm,
  sendBox,
  returnFocusRef,
  holdable,
  paused,
  stoppable,
  canDone,
  onClose,
  onClosed,
  onHold,
  onStop,
  onRequestDone,
}: {
  id: string;
  open: boolean;
  task: Task;
  project?: Project;
  tone: Tone;
  statusLabel: string;
  body: ReactNode;
  summary?: ReactNode;
  doneForm?: ReactNode;
  sendBox?: ReactNode;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
  holdable: boolean;
  paused: boolean;
  stoppable: boolean;
  canDone: boolean;
  onClose: () => void;
  /** Runs after the native dialog has closed. Return false to keep focus with a follow-up route. */
  onClosed?: () => boolean | void;
  onHold?: (pause: boolean) => void;
  onStop: () => void;
  onRequestDone: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const titleId = `${id}-title`;
  const closeTimer = useRef<number | undefined>(undefined);
  const openRef = useRef(open);
  const presentRef = useRef(open);
  const onClosedRef = useRef(onClosed);
  const [present, setPresent] = useState(open);
  const [exiting, setExiting] = useState(false);

  onClosedRef.current = onClosed;

  useEffect(() => {
    openRef.current = open;
    if (closeTimer.current !== undefined) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = undefined;
    }
    if (open) {
      if (!presentRef.current) {
        presentRef.current = true;
        setPresent(true);
      }
      setExiting(false);
      return;
    }
    if (!presentRef.current) return;
    setExiting(true);
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const finish = () => {
      closeTimer.current = undefined;
      if (openRef.current) return;
      const element = dialog.current;
      if (element?.open) element.close();
      presentRef.current = false;
      setPresent(false);
      setExiting(false);
      const restoreFocus = onClosedRef.current?.() !== false;
      if (restoreFocus) returnFocusRef.current?.focus({ preventScroll: true });
    };
    if (reduced) finish();
    else closeTimer.current = window.setTimeout(finish, 120);
  }, [open, returnFocusRef]);

  useEffect(() => {
    if (!open || !present) return;
    const element = dialog.current;
    if (!element) return;
    if (!element.open) element.showModal();
    closeButton.current?.focus({ preventScroll: true });
  }, [open, present]);

  useEffect(
    () => () => {
      if (closeTimer.current !== undefined) {
        window.clearTimeout(closeTimer.current);
        closeTimer.current = undefined;
      }
      const wasPresent = presentRef.current;
      const element = dialog.current;
      if (element?.open) element.close();
      if (wasPresent) returnFocusRef.current?.focus({ preventScroll: true });
    },
    [returnFocusRef],
  );

  if (!present || typeof document === "undefined") return null;

  const stopFromBackdrop = (event: MouseEvent<HTMLDialogElement>) => {
    if (event.target === event.currentTarget) onClose();
  };

  return createPortal(
    <dialog
      ref={dialog}
      className={`task-drawer-shell${exiting ? " is-closing" : ""}`}
      id={id}
      aria-labelledby={titleId}
      aria-modal="true"
      aria-hidden={exiting || undefined}
      inert={exiting ? true : undefined}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onClose();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") event.stopPropagation();
      }}
      onClick={stopFromBackdrop}
    >
      <div
        className="task-drawer-panel"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="task-drawer-header">
          <div className="task-drawer-heading">
            <div className="task-drawer-heading-meta">
              <StatusBadge tone={tone} label={statusLabel} />
              <span className="agent-code">{task.code}</span>
              {project?.name && (
                <span className="task-drawer-project">{project.name}</span>
              )}
            </div>
            <h2 id={titleId} title={task.title}>
              {task.title}
            </h2>
          </div>
          <div className="task-drawer-header-actions">
            {holdable && onHold && (
              <button
                className="icon-button"
                type="button"
                onClick={() => onHold(!paused)}
                title={t(paused ? "task.resume_detail" : "task.pause_detail")}
                aria-label={t(paused ? "task.resume" : "task.pause")}
              >
                {paused ? <CirclePlay size={15} /> : <CirclePause size={15} />}
              </button>
            )}
            {stoppable && (
              <button
                className="icon-button"
                type="button"
                onClick={onStop}
                title={t("task.stop")}
                aria-label={t("task.stop")}
              >
                <CircleStop size={15} />
              </button>
            )}
            {canDone && (
              <button
                className="icon-button"
                type="button"
                onClick={onRequestDone}
                title={t("task.mark_done_detail")}
                aria-label={t("task.mark_done")}
              >
                <CheckCircle2 size={15} />
              </button>
            )}
            <button
              ref={closeButton}
              className="icon-button task-drawer-close"
              type="button"
              onClick={onClose}
              title={t("common.close")}
              aria-label={t("common.close")}
            >
              <X size={18} />
            </button>
          </div>
        </header>
        <div className="task-drawer-scroll">
          <div className="task-drawer-sections">
            {summary && (
              <section className="task-drawer-section task-drawer-summary">
                {summary}
              </section>
            )}
            {doneForm && (
              <section className="task-drawer-section task-drawer-done-form">
                {doneForm}
              </section>
            )}
            <section className="task-drawer-section task-drawer-body">
              {body}
            </section>
            {sendBox && (
              <section className="task-drawer-section task-drawer-send">
                {sendBox}
              </section>
            )}
          </div>
        </div>
      </div>
    </dialog>,
    document.body,
  );
}

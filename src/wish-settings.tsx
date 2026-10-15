import { Settings, X } from "lucide-react";
import { createPortal } from "react-dom";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import { t } from "./i18n";

import "./wish-settings.css";

export function WishSettingsMenu({
  onOpenRights,
  onOpenAgents,
  rightsCount,
  agentsCount,
}: {
  onOpenRights: () => void;
  onOpenAgents: () => void;
  rightsCount?: number;
  agentsCount?: number;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const menuItemsRef = useRef<Array<HTMLButtonElement | null>>([]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !rootRef.current?.contains(event.target)
      )
        setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event.key === "Escape" &&
        rootRef.current?.contains(document.activeElement)
      ) {
        event.preventDefault();
        event.stopPropagation();
        setOpen(false);
        triggerRef.current?.focus({ preventScroll: true });
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  useEffect(() => {
    if (open) menuItemsRef.current[0]?.focus({ preventScroll: true });
  }, [open]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = menuItemsRef.current.filter(
      (item): item is HTMLButtonElement => !!item && !item.disabled,
    );
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      triggerRef.current?.focus({ preventScroll: true });
      return;
    }
    if (!items.length) return;
    const next =
      event.key === "ArrowDown"
        ? (current + 1 + items.length) % items.length
        : event.key === "ArrowUp"
          ? (current - 1 + items.length) % items.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? items.length - 1
              : undefined;
    if (next === undefined) return;
    event.preventDefault();
    items[next]?.focus({ preventScroll: true });
  };

  const choose = (action: () => void) => {
    setOpen(false);
    triggerRef.current?.focus({ preventScroll: true });
    action();
  };

  return (
    <div className="wish-settings-menu" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="workspace-settings-trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("workspace.settings")}
        title={t("workspace.settings")}
        onClick={() => setOpen((value) => !value)}
        onKeyDown={(event) => {
          if (
            !open &&
            (event.key === "ArrowDown" ||
              event.key === "Enter" ||
              event.key === " ")
          ) {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <Settings size={15} aria-hidden="true" />
      </button>
      {open && (
        <div
          ref={menuRef}
          className="wish-settings-menu-popover"
          role="menu"
          onKeyDown={onMenuKeyDown}
        >
          <button
            ref={(element) => {
              menuItemsRef.current[0] = element;
            }}
            type="button"
            role="menuitem"
            onClick={() => choose(onOpenRights)}
          >
            <span>{t("rights.title")}</span>
            {rightsCount !== undefined && <span>{rightsCount}</span>}
          </button>
          <button
            ref={(element) => {
              menuItemsRef.current[1] = element;
            }}
            type="button"
            role="menuitem"
            onClick={() => choose(onOpenAgents)}
          >
            <span>{t("tabs.agents")}</span>
            {agentsCount !== undefined && <span>{agentsCount}</span>}
          </button>
        </div>
      )}
    </div>
  );
}

export function SettingsDrawer({
  title,
  children,
  closeRequest,
  onClose,
  onClosed,
}: {
  title: string;
  children: ReactNode;
  /** Increment to request an animated close before a follow-up route. */
  closeRequest?: number;
  onClose: () => void;
  /** Runs after the native dialog has closed. Return false to keep focus with a follow-up route. */
  onClosed?: () => boolean | void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  const closeRequestRef = useRef<number | undefined>(closeRequest);
  const onClosedRef = useRef(onClosed);
  const finishedRef = useRef(false);
  const [closing, setClosing] = useState(false);

  onClosedRef.current = onClosed;

  const finishClose = useCallback(() => {
    if (closeTimer.current !== undefined) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = undefined;
    }
    if (finishedRef.current) return;
    finishedRef.current = true;
    if (dialogRef.current?.open) dialogRef.current.close();
    onClose();
    const restoreFocus = onClosedRef.current?.() !== false;
    if (restoreFocus) previousFocus.current?.focus({ preventScroll: true });
  }, [onClose]);

  const requestClose = useCallback(() => {
    if (closing) return;
    setClosing(true);
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      finishClose();
      return;
    }
    closeTimer.current = window.setTimeout(finishClose, 120);
  }, [closing, finishClose]);

  useEffect(() => {
    previousFocus.current = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (!dialog.open) dialog.showModal();
    closeRef.current?.focus({ preventScroll: true });
    return () => {
      if (closeTimer.current !== undefined) {
        window.clearTimeout(closeTimer.current);
        closeTimer.current = undefined;
      }
      if (dialog.open) dialog.close();
      if (!finishedRef.current)
        previousFocus.current?.focus({ preventScroll: true });
    };
  }, []);

  useEffect(() => {
    if (
      closeRequest === undefined ||
      closeRequestRef.current === undefined ||
      closeRequest === closeRequestRef.current
    ) {
      closeRequestRef.current = closeRequest;
      return;
    }
    closeRequestRef.current = closeRequest;
    requestClose();
  }, [closeRequest, requestClose]);

  if (typeof document === "undefined") return null;
  return createPortal(
    <dialog
      ref={dialogRef}
      className={`wish-settings-drawer${closing ? " is-closing" : ""}`}
      aria-label={title}
      aria-modal="true"
      aria-hidden={closing || undefined}
      inert={closing ? true : undefined}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        requestClose();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") event.stopPropagation();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) requestClose();
      }}
    >
      <div className="wish-settings-drawer-panel">
        <header className="wish-settings-drawer-header">
          <h2>{title}</h2>
          <button
            ref={closeRef}
            type="button"
            className="icon-button"
            aria-label={t("common.close")}
            title={t("common.close")}
            onClick={requestClose}
          >
            <X size={17} aria-hidden="true" />
          </button>
        </header>
        <div className="wish-settings-drawer-content">{children}</div>
      </div>
    </dialog>,
    document.body,
  );
}

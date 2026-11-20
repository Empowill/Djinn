import { createPortal } from "react-dom";
import {
  useId,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type FocusEvent,
  type PointerEvent,
  type ReactNode,
} from "react";

export type PlatformShortcut = {
  label: string;
  aria: string;
};

// isMac reports whether the interface runs on macOS, to show Mac modifier symbols (__DJINN_MAC__ sets it in tests).
export function isMac(): boolean {
  const g = globalThis as unknown as { __DJINN_MAC__?: boolean };
  if (typeof g.__DJINN_MAC__ === "boolean") {
    return g.__DJINN_MAC__;
  }
  if (typeof navigator !== "undefined") {
    const nav = navigator as unknown as {
      userAgentData?: { platform?: string };
      platform?: string;
      userAgent?: string;
    };
    const p =
      nav.userAgentData?.platform || nav.platform || nav.userAgent || "";
    return /Mac/i.test(p);
  }
  if (typeof process !== "undefined" && process.platform === "darwin") {
    return true;
  }
  return false;
}

// platformShortcut names a shortcut as the platform writes it: ⌘ on a Mac, Ctrl elsewhere; aria is for
// aria-keyshortcuts.
export function platformShortcut(
  key: string,
  displayKey = key,
): PlatformShortcut {
  return isMac()
    ? { label: `⌘ ${displayKey}`, aria: `Meta+${key}` }
    : { label: `Ctrl ${displayKey}`, aria: `Control+${key}` };
}

type TooltipButtonProps = Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  "aria-describedby" | "aria-label" | "title"
> & {
  label: string;
  tooltip?: string;
  // Lines under the tooltip's text, in mono: the running Djinn's version and where it comes from (W61).
  detail?: string[];
  shortcut?: string;
  children?: ReactNode;
};

// useTooltip shows a tooltip under its anchor, or over it when the window has no room below, while the pointer is on
// it or it has the focus; Escape hides it. It lives in document.body, so no clipped surface hides it.
function useTooltip<T extends HTMLElement>(
  tooltip: string | undefined,
  shortcut: string | undefined,
  detail: string[] | undefined,
) {
  const anchorRef = useRef<T>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const tooltipId = useId();
  const lines = detail?.join("\n");

  useLayoutEffect(() => {
    if (!open) return;
    const anchor = anchorRef.current;
    const flyout = tooltipRef.current;
    if (!anchor || !flyout) return;

    const anchorBox = anchor.getBoundingClientRect();
    const flyoutBox = flyout.getBoundingClientRect();
    const margin = 8;
    const maxLeft = Math.max(
      margin,
      window.innerWidth - flyoutBox.width - margin,
    );
    const left = Math.min(
      maxLeft,
      Math.max(
        margin,
        anchorBox.left + (anchorBox.width - flyoutBox.width) / 2,
      ),
    );
    const below = anchorBox.bottom + margin;
    const top =
      below + flyoutBox.height <= window.innerHeight - margin
        ? below
        : Math.max(margin, anchorBox.top - flyoutBox.height - margin);
    setPosition({ left, top, visibility: "visible" });
  }, [layoutVersion, open, shortcut, tooltip, lines]);

  useEffect(() => {
    if (!open) return;
    const reposition = () => setLayoutVersion((version) => version + 1);
    const dismiss = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    window.addEventListener("keydown", dismiss);
    return () => {
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
      window.removeEventListener("keydown", dismiss);
    };
  }, [open]);

  const show = () => {
    if (open) return;
    setPosition(null);
    setOpen(true);
  };
  const hide = () => setOpen(false);
  const leave = () => {
    if (document.activeElement !== anchorRef.current) hide();
  };

  const flyout =
    open &&
    (tooltip || shortcut) &&
    createPortal(
      <div
        ref={tooltipRef}
        id={tooltipId}
        role="tooltip"
        // No words, its shortcut alone: a small pill.
        className={tooltip ? "app-tooltip" : "app-tooltip key-only"}
        style={position ?? { visibility: "hidden" }}
      >
        {tooltip && (
          <span>
            {tooltip}
            {detail?.map((line, i) => (
              <span key={i} className="app-tooltip-line">
                {line}
              </span>
            ))}
          </span>
        )}
        {shortcut && <kbd className="key-hint">{shortcut}</kbd>}
      </div>,
      document.body,
    );

  return {
    anchorRef,
    describedBy: open ? tooltipId : undefined,
    show,
    hide,
    leave,
    flyout,
  };
}

export function TooltipButton({
  label,
  tooltip = label,
  detail,
  shortcut,
  children,
  type = "button",
  onBlur,
  onFocus,
  onPointerEnter,
  onPointerLeave,
  ...props
}: TooltipButtonProps) {
  const tip = useTooltip<HTMLButtonElement>(tooltip, shortcut, detail);
  const handleFocus = (event: FocusEvent<HTMLButtonElement>) => {
    onFocus?.(event);
    tip.show();
  };
  const handleBlur = (event: FocusEvent<HTMLButtonElement>) => {
    onBlur?.(event);
    tip.hide();
  };
  const handlePointerEnter = (event: PointerEvent<HTMLButtonElement>) => {
    onPointerEnter?.(event);
    tip.show();
  };
  const handlePointerLeave = (event: PointerEvent<HTMLButtonElement>) => {
    onPointerLeave?.(event);
    tip.leave();
  };

  return (
    <>
      <button
        {...props}
        type={type}
        ref={tip.anchorRef}
        aria-label={label}
        aria-describedby={tip.describedBy}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onPointerEnter={handlePointerEnter}
        onPointerLeave={handlePointerLeave}
      >
        {children}
      </button>
      {tip.flyout}
    </>
  );
}

// TooltipLabel is a word that says more under the pointer, as the EARLY ACCESS tag by the logo says the version
// (L10 A). It takes no focus: what it says is said too by a control the keyboard reaches.
export function TooltipLabel({
  className,
  tooltip,
  detail,
  children,
}: {
  className?: string;
  tooltip: string;
  detail?: string[];
  children: ReactNode;
}) {
  const tip = useTooltip<HTMLSpanElement>(tooltip, undefined, detail);
  return (
    <>
      <span
        className={className}
        ref={tip.anchorRef}
        aria-describedby={tip.describedBy}
        onPointerEnter={tip.show}
        onPointerLeave={tip.leave}
      >
        {children}
      </span>
      {tip.flyout}
    </>
  );
}

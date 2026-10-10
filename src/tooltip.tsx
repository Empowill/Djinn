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

function isMacPlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac/i.test(`${navigator.platform} ${navigator.userAgent}`);
}

export function platformShortcut(
  key: string,
  displayKey = key,
): PlatformShortcut {
  return isMacPlatform()
    ? { label: `⌘ ${displayKey}`, aria: `Meta+${key}` }
    : { label: `Ctrl ${displayKey}`, aria: `Control+${key}` };
}

type TooltipButtonProps = Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  "aria-describedby" | "aria-label" | "title"
> & {
  label: string;
  tooltip?: string;
  shortcut?: string;
  children?: ReactNode;
};

export function TooltipButton({
  label,
  tooltip = label,
  shortcut,
  children,
  type = "button",
  onBlur,
  onFocus,
  onPointerEnter,
  onPointerLeave,
  ...props
}: TooltipButtonProps) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const tooltipId = useId();

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
  }, [layoutVersion, open, shortcut, tooltip]);

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
  const handleFocus = (event: FocusEvent<HTMLButtonElement>) => {
    onFocus?.(event);
    show();
  };
  const handleBlur = (event: FocusEvent<HTMLButtonElement>) => {
    onBlur?.(event);
    hide();
  };
  const handlePointerEnter = (event: PointerEvent<HTMLButtonElement>) => {
    onPointerEnter?.(event);
    show();
  };
  const handlePointerLeave = (event: PointerEvent<HTMLButtonElement>) => {
    onPointerLeave?.(event);
    if (document.activeElement !== anchorRef.current) hide();
  };

  return (
    <>
      <button
        {...props}
        type={type}
        ref={anchorRef}
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onPointerEnter={handlePointerEnter}
        onPointerLeave={handlePointerLeave}
      >
        {children}
      </button>
      {open &&
        createPortal(
          <div
            ref={tooltipRef}
            id={tooltipId}
            role="tooltip"
            className="app-tooltip"
            style={position ?? { visibility: "hidden" }}
          >
            <span>{tooltip}</span>
            {shortcut && <kbd>{shortcut}</kbd>}
          </div>,
          document.body,
        )}
    </>
  );
}

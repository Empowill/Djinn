import { ChevronDown } from "lucide-react";
import {
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  useEffect,
  useRef,
  useState,
} from "react";

// The compact creation tags all use the same trigger and popover behavior. Keeping the menu generic lets project
// selection retain its multi-select checkboxes while provider and allowance choices use the same keyboard-friendly
// surface.
export function WishDropdown({
  label,
  icon,
  value,
  children,
  className = "",
  disabled = false,
}: {
  label: string;
  icon?: ReactNode;
  value: ReactNode;
  children: ReactNode;
  className?: string;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
    };
  }, [open]);

  const handleMenuClick = (event: MouseEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest("[data-wish-dropdown-close]")) {
      setOpen(false);
      trigger.current?.focus({ preventScroll: true });
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!open || event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    setOpen(false);
    trigger.current?.focus({ preventScroll: true });
  };

  return (
    <div
      ref={root}
      className={`wish-dropdown ${open ? "is-open" : ""} ${className}`.trim()}
      onKeyDown={handleKeyDown}
    >
      <button
        ref={trigger}
        type="button"
        className="wish-tag wish-dropdown-trigger"
        aria-label={label}
        aria-expanded={open}
        aria-haspopup="dialog"
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
      >
        <span className="wish-dropdown-value">
          {icon && (
            <span className="wish-dropdown-icon" aria-hidden="true">
              {icon}
            </span>
          )}
          <span className="wish-dropdown-label">{value}</span>
        </span>
        <ChevronDown size={13} aria-hidden="true" />
      </button>
      {open && (
        <div
          className="wish-dropdown-menu wish-tag-menu"
          role="dialog"
          aria-label={label}
          onClick={handleMenuClick}
        >
          {children}
        </div>
      )}
    </div>
  );
}

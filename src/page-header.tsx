import type { ComponentPropsWithoutRef, ReactNode } from "react";

import { TooltipButton } from "./tooltip";

// PageHeader is the top of a page, aligned with the window's toolbar: where you are, the operating-load gauge in the
// middle, the page's actions.
export function PageHeader({
  children,
  middle,
  actions,
}: {
  children?: ReactNode;
  middle?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="topbar page-header">
      <div className="breadcrumbs">{children}</div>
      {middle}
      {actions && <div className="topbar-actions">{actions}</div>}
    </header>
  );
}

type PageHeaderActionProps = Omit<
  ComponentPropsWithoutRef<"button">,
  "aria-label" | "className" | "title" | "type"
> & {
  label: string;
  title?: string;
  shortcut?: string;
  className?: string;
};

export function PageHeaderAction({
  label,
  title = label,
  shortcut,
  className = "",
  children,
  ...props
}: PageHeaderActionProps) {
  return (
    <TooltipButton
      {...props}
      type="button"
      className={`button secondary page-header-action ${className}`.trim()}
      label={label}
      tooltip={title}
      shortcut={shortcut}
    >
      {children}
    </TooltipButton>
  );
}

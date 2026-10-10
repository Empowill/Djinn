import type { ComponentPropsWithoutRef, ReactNode } from "react";

import { TooltipButton } from "./tooltip";

export function PageHeader({
  children,
  actions,
}: {
  children: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="topbar page-header">
      <div className="breadcrumbs">{children}</div>
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

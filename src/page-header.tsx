import type { ComponentPropsWithoutRef, ReactNode } from "react";

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
  className?: string;
};

export function PageHeaderAction({
  label,
  title = label,
  className = "",
  children,
  ...props
}: PageHeaderActionProps) {
  return (
    <button
      {...props}
      type="button"
      className={`button secondary page-header-action ${className}`.trim()}
      aria-label={label}
      title={title}
    >
      {children}
    </button>
  );
}

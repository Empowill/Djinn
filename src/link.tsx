// A link an agent wrote: a djinn:// one opens what it names in place, a tilasm or a wish; in the window, the system's
// browser opens an http(s) one; anything else stays text. A click on it does only that: inside a question's option it
// never picks the option.
import { ExternalLink } from "lucide-react";
import { type MouseEvent, type ReactNode } from "react";

import { useDjinn } from "./data/djinn";
import { isDjinnLink, linkName, openDjinnLink } from "./data/links";
import { t } from "./i18n";

export function Link({
  href,
  className,
  children,
}: {
  href?: string;
  className?: string;
  children?: ReactNode;
}) {
  const djinn = useDjinn();
  if (href && isDjinnLink(href))
    return (
      <a
        href={href}
        className={className ? `djinn-link ${className}` : "djinn-link"}
        onClick={(e: MouseEvent) => {
          e.preventDefault();
          e.stopPropagation();
          if (djinn) void openDjinnLink(djinn, href);
        }}
      >
        {children}
      </a>
    );
  if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>;
  return (
    <a
      href={href}
      className={className}
      target="_blank"
      rel="noopener noreferrer"
      onClick={(e: MouseEvent) => {
        e.stopPropagation();
        if (!djinn) return;
        e.preventDefault();
        void djinn.clients.ui
          .openExternal({ url: href })
          .catch(() => window.open(href, "_blank", "noopener"));
      }}
    >
      {children}
    </a>
  );
}

// linkLabel says what a link names, for a button: "PR #12", "MR !123", or its host.
export function linkLabel(href: string): string {
  const name = linkName(href);
  switch (name.kind) {
    case "pr":
      return t("link.pr", { number: name.number });
    case "mr":
      return t("link.mr", { number: name.number });
    case "issue":
      return t("link.issue", { number: name.number });
    case "pipeline":
      return t("link.pipeline", { number: name.number });
    case "run":
      return t("link.run");
    case "tilasm":
      return t("link.tilasm");
    case "wish":
      return t("link.wish");
    case "host":
      return name.host;
  }
}

// MoveLink is the main link of a move, where the developer acts, as a button: "Open MR !123".
export function MoveLink({ href }: { href: string }) {
  const label = t("link.open", { name: linkLabel(href) });
  return (
    <Link href={href} className="button secondary small move-link">
      <ExternalLink size={13} aria-hidden="true" />
      <span>{label}</span>
    </Link>
  );
}

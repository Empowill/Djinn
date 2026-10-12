// A command an agent wrote for the developer to run, in a question's option or context: its text selectable, and a
// small button that copies it exactly, saying so for a moment. A click on it copies only: it never picks an option
// nor answers the question around it.
import { Check, Copy } from "lucide-react";
import { type MouseEvent, memo, useEffect, useState } from "react";

import { commandParts } from "./data/text";
import { t } from "./i18n";
import { Link } from "./link";

// How long the button says it copied.
const COPIED_MS = 1500;

export const CopyButton = memo(function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState<"copied" | "failed" | "">("");
  useEffect(() => {
    if (!done) return;
    const timer = setTimeout(() => setDone(""), COPIED_MS);
    return () => clearTimeout(timer);
  }, [done]);
  const copy = (e: MouseEvent) => {
    // Inside an option, the click would pick it.
    e.stopPropagation();
    e.preventDefault();
    // A window with no clipboard says the copy failed, the text still selectable.
    void Promise.resolve()
      .then(() => navigator.clipboard.writeText(text))
      .then(() => setDone("copied"))
      .catch(() => setDone("failed"));
  };
  const label =
    done === "copied"
      ? t("markdown.copied")
      : done === "failed"
        ? t("markdown.copy_failed")
        : t("markdown.copy_command");
  return (
    <button
      type="button"
      className={`copy-command${done ? ` ${done}` : ""}`}
      title={label}
      aria-label={label}
      onClick={copy}
    >
      {done === "copied" ? (
        <Check size={12} aria-hidden="true" />
      ) : (
        <Copy size={12} aria-hidden="true" />
      )}
      {done && (
        <span className="copy-feedback" role="status">
          {label}
        </span>
      )}
    </button>
  );
});

// Command is one command in a line of text: its code, then its copy button.
export function Command({ text }: { text: string }) {
  return (
    <span className="command">
      <code>{text}</code>
      <CopyButton text={text} />
    </span>
  );
}

// CommandText is a line of plain text, a question's title or option, its commands copyable and its links clickable
// (src/data/text.ts).
export const CommandText = memo(function CommandText({
  text,
}: {
  text: string;
}) {
  return (
    <>
      {commandParts(text).map((part, i) =>
        part.code ? (
          <Command key={i} text={part.text} />
        ) : part.href ? (
          <Link key={i} href={part.text} className="text-link">
            {part.text}
          </Link>
        ) : (
          part.text
        ),
      )}
    </>
  );
});

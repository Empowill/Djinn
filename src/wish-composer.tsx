import { Bold, Italic, List, ListOrdered } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { t } from "./i18n";
import { composerMarkdown } from "./wish-composer-markdown";

type Command = "bold" | "italic" | "insertUnorderedList" | "insertOrderedList";

// The browser owns the editable DOM and its undo stack. React owns the serialized draft, never the editor's
// children: checking an agent or changing a project must not move the caret or replace the text.
export function WishComposer({
  id,
  describedBy,
  disabled,
  invalid = false,
  onChange,
}: {
  id: string;
  describedBy: string;
  disabled: boolean;
  invalid?: boolean;
  onChange: (markdown: string, hasText: boolean) => void;
}) {
  const editor = useRef<HTMLDivElement>(null);
  const selection = useRef<Range | null>(null);
  const [empty, setEmpty] = useState(true);
  const [active, setActive] = useState<Partial<Record<Command, boolean>>>({});
  const commands = [
    { command: "bold" as const, label: t("make.bold"), icon: Bold },
    { command: "italic" as const, label: t("make.italic"), icon: Italic },
    {
      command: "insertUnorderedList" as const,
      label: t("make.bullets"),
      icon: List,
    },
    {
      command: "insertOrderedList" as const,
      label: t("make.numbered"),
      icon: ListOrdered,
    },
  ];
  const update = () => {
    if (!editor.current) return;
    const hasText = Boolean(
      // Spaces and invisible Unicode formatting controls alone do not make a request.
      editor.current.textContent?.replace(/[\s\p{Cf}]/gu, ""),
    );
    setEmpty(!hasText);
    onChange(composerMarkdown(editor.current), hasText);
  };
  useEffect(() => {
    const remember = () => {
      const current = window.getSelection();
      if (
        !current?.rangeCount ||
        !editor.current?.contains(current.anchorNode) ||
        !editor.current.contains(current.focusNode)
      )
        return;
      selection.current = current.getRangeAt(0).cloneRange();
      setActive(
        Object.fromEntries(
          commands.map(({ command }) => [
            command,
            document.queryCommandState(command),
          ]),
        ),
      );
    };
    document.addEventListener("selectionchange", remember);
    return () => document.removeEventListener("selectionchange", remember);
  }, []);
  const format = (command: Command) => {
    if (disabled || !editor.current) return;
    const current = window.getSelection();
    // Read a current editor selection synchronously: selectionchange may still be queued when a shortcut fires.
    // The saved range is only for a toolbar button reached by keyboard, which can move focus outside the editor.
    const range =
      current?.rangeCount &&
      editor.current.contains(current.anchorNode) &&
      editor.current.contains(current.focusNode)
        ? current.getRangeAt(0).cloneRange()
        : selection.current;
    editor.current.focus();
    if (range && editor.current.contains(range.commonAncestorContainer)) {
      current?.removeAllRanges();
      current?.addRange(range);
    }
    // Native commands preserve undo/redo and work in Chromium and the Wails WebKit window.
    document.execCommand(command, false);
    setActive((previous) => ({
      ...previous,
      [command]: document.queryCommandState(command),
    }));
    update();
  };
  const insertText = (text: string) => {
    document.execCommand(
      "insertText",
      false,
      text.replace(/\r\n?/g, "\n").replace(/\0/g, ""),
    );
    update();
  };
  return (
    <div className={`wish-composer ${disabled ? "is-disabled" : ""}`}>
      <div
        className="wish-composer-tools"
        role="group"
        aria-label={t("make.formatting")}
      >
        {commands.map(({ command, label, icon: Icon }) => (
          <button
            key={command}
            type="button"
            title={label}
            aria-label={label}
            aria-pressed={Boolean(active[command])}
            aria-controls={id}
            disabled={disabled}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => format(command)}
          >
            <Icon size={16} />
          </button>
        ))}
      </div>
      <div
        ref={editor}
        id={id}
        role="textbox"
        aria-multiline="true"
        aria-required="true"
        aria-disabled={disabled}
        aria-invalid={invalid || undefined}
        aria-labelledby={`${id}-label`}
        aria-describedby={describedBy}
        contentEditable={!disabled}
        suppressContentEditableWarning
        tabIndex={0}
        className="wish-composer-input"
        data-empty={empty}
        data-placeholder={t("make.what_placeholder")}
        onInput={update}
        onPaste={(event) => {
          event.preventDefault();
          if (!disabled) insertText(event.clipboardData.getData("text/plain"));
        }}
        onDrop={(event) => {
          // Do not let the browser insert dragged HTML, images or files into the prompt.
          event.preventDefault();
        }}
        onKeyDown={(event) => {
          if (
            event.nativeEvent.isComposing ||
            !(event.metaKey || event.ctrlKey) ||
            event.altKey
          )
            return;
          const key = event.key.toLowerCase();
          const command =
            key === "b"
              ? "bold"
              : key === "i"
                ? "italic"
                : event.shiftKey && (key === "7" || event.code === "Digit7")
                  ? "insertOrderedList"
                  : event.shiftKey && (key === "8" || event.code === "Digit8")
                    ? "insertUnorderedList"
                    : undefined;
          if (command) {
            event.preventDefault();
            format(command);
          }
        }}
      />
    </div>
  );
}

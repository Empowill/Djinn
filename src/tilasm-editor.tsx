// The editor of a Markdown tilasm (src/tilasms.tsx): Tiptap on the panel's surface, with what a document editor
// needs and no chrome of its own. A bar near the selection formats it, links it and changes its block; "/" at a block's
// start or after a space opens a menu of blocks by the caret; a handle in the left gutter moves a whole block, the
// drop shown by a line, in one step that undo takes back. What it reads and writes is Markdown
// (src/tilasm-markdown.ts).
import { type Editor } from "@tiptap/core";
import { NodeSelection } from "@tiptap/pm/state";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import { BubbleMenu } from "@tiptap/react/menus";
import {
  Bold,
  ChevronDown,
  Code,
  GripVertical,
  Heading1,
  Heading2,
  Heading3,
  Italic,
  Link,
  List,
  ListOrdered,
  ListTodo,
  Minus,
  Pilcrow,
  Quote,
  SquareCode,
  Strikethrough,
  Table,
  Unlink,
} from "lucide-react";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

import { t } from "./i18n";
import { tilasmExtensions } from "./tilasm-markdown";

// useTilasmEditor makes the editor of a document, read only until told otherwise. onEdit hears each edit the
// developer makes, never the content the page sets.
export function useTilasmEditor(onEdit: () => void): Editor | null {
  const edit = useRef(onEdit);
  useLayoutEffect(() => {
    edit.current = onEdit;
  });
  return useEditor(
    {
      extensions: tilasmExtensions({
        placeholder: t("tilasms.placeholder"),
        emptyNodeClass: "tilasm-empty-block",
      }),
      editable: false,
      immediatelyRender: false,
      shouldRerenderOnTransaction: false,
      editorProps: {
        attributes: {
          class: "tilasm-prose",
          spellcheck: "true",
          "aria-label": t("tilasms.body"),
        },
      },
      onUpdate: () => edit.current(),
    },
    [],
  );
}

// The blocks the "/" menu and the bar offer, each with words to find it by in any language.
type BlockId =
  | "paragraph"
  | "heading1"
  | "heading2"
  | "heading3"
  | "bulletList"
  | "orderedList"
  | "taskList"
  | "blockquote"
  | "codeBlock"
  | "horizontalRule"
  | "table";

interface BlockItem {
  id: BlockId;
  label: string;
  words: string;
  icon: ReactNode;
}

function blockItems(): BlockItem[] {
  const icon = (Icon: typeof Pilcrow) => <Icon size={14} aria-hidden="true" />;
  return [
    {
      id: "paragraph",
      label: t("tilasms.block.paragraph"),
      words: "text paragraph p",
      icon: icon(Pilcrow),
    },
    {
      id: "heading1",
      label: t("tilasms.block.heading1"),
      words: "h1 heading title",
      icon: icon(Heading1),
    },
    {
      id: "heading2",
      label: t("tilasms.block.heading2"),
      words: "h2 heading subtitle",
      icon: icon(Heading2),
    },
    {
      id: "heading3",
      label: t("tilasms.block.heading3"),
      words: "h3 heading",
      icon: icon(Heading3),
    },
    {
      id: "bulletList",
      label: t("tilasms.block.bullet"),
      words: "ul bullet list",
      icon: icon(List),
    },
    {
      id: "orderedList",
      label: t("tilasms.block.ordered"),
      words: "ol numbered ordered list",
      icon: icon(ListOrdered),
    },
    {
      id: "taskList",
      label: t("tilasms.block.task"),
      words: "todo task checkbox list",
      icon: icon(ListTodo),
    },
    {
      id: "blockquote",
      label: t("tilasms.block.quote"),
      words: "quote blockquote",
      icon: icon(Quote),
    },
    {
      id: "codeBlock",
      label: t("tilasms.block.code"),
      words: "code pre snippet",
      icon: icon(SquareCode),
    },
    {
      id: "horizontalRule",
      label: t("tilasms.block.rule"),
      words: "hr divider rule line separator",
      icon: icon(Minus),
    },
    {
      id: "table",
      label: t("tilasms.block.table"),
      words: "table grid",
      icon: icon(Table),
    },
  ];
}

// plain folds case and accents, so that a word typed without them finds a label written with them.
const plain = (text: string) =>
  text.normalize("NFD").replace(/\p{M}/gu, "").toLocaleLowerCase();

// The blocks a "/" query finds, by their label or their words.
function matching(items: BlockItem[], query: string): BlockItem[] {
  const q = plain(query);
  if (!q) return items;
  return items.filter((item) =>
    plain(`${item.label} ${item.words}`)
      .split(/\s+/)
      .some((word) => word.startsWith(q)),
  );
}

// The block the selection is in, as the bar names it.
function currentBlock(editor: Editor): BlockId {
  if (editor.isActive("heading", { level: 1 })) return "heading1";
  if (editor.isActive("heading", { level: 2 })) return "heading2";
  if (editor.isActive("heading", { level: 3 })) return "heading3";
  if (editor.isActive("taskList")) return "taskList";
  if (editor.isActive("bulletList")) return "bulletList";
  if (editor.isActive("orderedList")) return "orderedList";
  if (editor.isActive("blockquote")) return "blockquote";
  if (editor.isActive("codeBlock")) return "codeBlock";
  return "paragraph";
}

// turnInto makes the selection's block the one picked; a block in a list or a quote leaves it first, so that a list
// turned into a heading is a heading.
function turnInto(editor: Editor, id: BlockId) {
  const chain = editor.chain().focus();
  const from = currentBlock(editor);
  if (from === id) return;
  if (from === "bulletList") chain.toggleBulletList();
  if (from === "orderedList") chain.toggleOrderedList();
  if (from === "taskList") chain.toggleTaskList();
  if (from === "blockquote") chain.toggleBlockquote();
  run(chain, id);
}

type Chain = ReturnType<Editor["chain"]>;

function run(chain: Chain, id: BlockId) {
  if (id === "paragraph") chain.setParagraph();
  if (id === "heading1") chain.setHeading({ level: 1 });
  if (id === "heading2") chain.setHeading({ level: 2 });
  if (id === "heading3") chain.setHeading({ level: 3 });
  if (id === "bulletList") chain.toggleBulletList();
  if (id === "orderedList") chain.toggleOrderedList();
  if (id === "taskList") chain.toggleTaskList();
  if (id === "blockquote") chain.toggleBlockquote();
  if (id === "codeBlock") chain.toggleCodeBlock();
  if (id === "horizontalRule") chain.setHorizontalRule();
  if (id === "table")
    chain.insertTable({ rows: 3, cols: 3, withHeaderRow: true });
  chain.run();
}

// scrollParent is the nearest box that scrolls the editor: the bar and the menu follow its scroll.
function scrollParent(element: HTMLElement | null): HTMLElement | Window {
  for (let at = element?.parentElement; at; at = at.parentElement) {
    const { overflowY } = getComputedStyle(at);
    if (overflowY === "auto" || overflowY === "scroll") return at;
  }
  return window;
}

// TilasmEditor shows the document's text with its bar, its "/" menu and its block handle: those only while it is
// editable.
export function TilasmEditor({
  editor,
  editable,
}: {
  editor: Editor | null;
  editable: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [scroller, setScroller] = useState<HTMLElement | Window>();
  useEffect(() => setScroller(scrollParent(box.current)), []);
  return (
    <div className="tilasm-editor" ref={box}>
      <EditorContent editor={editor} />
      {editor && editable && scroller && (
        <>
          <SelectionBar editor={editor} scroller={scroller} />
          <SlashMenu editor={editor} />
          <BlockHandle editor={editor} box={box} />
        </>
      )}
    </div>
  );
}

// The top-level block at a height of the page, by its index in the document, and its box.
function blockAt(editor: Editor, y: number) {
  const view = editor.view;
  const page = view.dom.getBoundingClientRect();
  const top = Math.min(Math.max(y, page.top + 1), page.bottom - 1);
  const at = view.posAtCoords({ left: page.left + 1, top });
  if (!at) return null;
  const doc = view.state.doc;
  const inside = at.inside >= 0 ? at.inside : at.pos;
  const index = doc.resolve(Math.min(inside, doc.content.size)).index(0);
  return blockBox(editor, Math.min(index, doc.childCount - 1));
}

function blockBox(editor: Editor, index: number) {
  const doc = editor.view.state.doc;
  if (index < 0 || index >= doc.childCount) return null;
  let from = 0;
  for (let i = 0; i < index; i++) from += doc.child(i).nodeSize;
  const dom = editor.view.nodeDOM(from);
  if (!(dom instanceof HTMLElement)) return null;
  return { index, from, rect: dom.getBoundingClientRect() };
}

// moveBlock moves the top-level block at index before the one at boundary (the end for childCount): one transaction,
// undone at once.
function moveBlock(editor: Editor, index: number, boundary: number) {
  const { state } = editor.view;
  const doc = state.doc;
  if (boundary === index || boundary === index + 1) return;
  const source = blockBox(editor, index);
  if (!source) return;
  const node = doc.child(index);
  const tr = state.tr.delete(source.from, source.from + node.nodeSize);
  const to = boundary > index ? boundary - 1 : boundary;
  let at = 0;
  for (let i = 0; i < to; i++) at += tr.doc.child(i).nodeSize;
  tr.insert(at, node);
  editor.view.dispatch(tr.scrollIntoView());
}

// BlockHandle is the handle by the block under the pointer, in the left gutter. Dragged, a line shows where the block
// lands; Escape cancels. Focused, the arrows move its block up and down.
function BlockHandle({
  editor,
  box,
}: {
  editor: Editor;
  box: RefObject<HTMLDivElement | null>;
}) {
  // The block the handle stands by, and where the drag would drop it: a boundary between blocks.
  const [hover, setHover] = useState<{ index: number; top: number }>();
  const [drop, setDrop] = useState<{ boundary: number; top: number }>();
  const drag = useRef<{ index: number; pointer: number } | null>(null);
  const target = useRef<number | null>(null);
  const place = useCallback(
    (index: number) => {
      const found = blockBox(editor, index);
      const frame = box.current?.getBoundingClientRect();
      if (!found || !frame) return setHover(undefined);
      setHover({ index, top: found.rect.top - frame.top });
    },
    [editor, box],
  );
  useEffect(() => {
    const frame = box.current;
    if (!frame) return;
    const move = (event: PointerEvent) => {
      const bounds = frame.getBoundingClientRect();
      if (drag.current) {
        if (event.pointerId !== drag.current.pointer) return;
        target.current = null;
        const found = blockAt(editor, event.clientY);
        if (!found) return setDrop(undefined);
        const after = event.clientY > found.rect.top + found.rect.height / 2;
        const boundary = found.index + (after ? 1 : 0);
        const { index } = drag.current;
        if (boundary === index || boundary === index + 1)
          return setDrop(undefined);
        target.current = boundary;
        setDrop({
          boundary,
          top: (after ? found.rect.bottom : found.rect.top) - bounds.top,
        });
        return;
      }
      if (
        event.clientY < bounds.top ||
        event.clientY > bounds.bottom ||
        event.clientX < bounds.left ||
        event.clientX > bounds.right
      )
        return;
      const found = blockAt(editor, event.clientY);
      if (found)
        setHover({ index: found.index, top: found.rect.top - bounds.top });
    };
    const end = (event: PointerEvent) => {
      const current = drag.current;
      if (!current || event.pointerId !== current.pointer) return;
      drag.current = null;
      document.body.classList.remove("tilasm-dragging");
      const boundary = target.current;
      target.current = null;
      setDrop(undefined);
      if (boundary !== null && event.type === "pointerup")
        moveBlock(editor, current.index, boundary);
    };
    const cancel = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || !drag.current) return;
      drag.current = null;
      target.current = null;
      document.body.classList.remove("tilasm-dragging");
      setDrop(undefined);
    };
    const leave = () => {
      if (!drag.current) setHover(undefined);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
    window.addEventListener("keydown", cancel, true);
    frame.addEventListener("pointerleave", leave);
    // The page's text moves under a still pointer: the handle follows its block.
    const follow = () => setHover(undefined);
    editor.on("update", follow);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
      window.removeEventListener("keydown", cancel, true);
      frame.removeEventListener("pointerleave", leave);
      editor.off("update", follow);
      document.body.classList.remove("tilasm-dragging");
    };
  }, [editor, box]);
  if (!hover) return null;
  return (
    <>
      <button
        type="button"
        className="tilasm-drag-handle"
        style={{ top: hover.top }}
        title={t("tilasms.move_block")}
        aria-label={t("tilasms.move_block")}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.preventDefault();
          drag.current = { index: hover.index, pointer: event.pointerId };
          document.body.classList.add("tilasm-dragging");
        }}
        onKeyDown={(event) => {
          if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
          event.preventDefault();
          const up = event.key === "ArrowUp";
          const to = up ? hover.index - 1 : hover.index + 2;
          if (to < 0 || to > editor.state.doc.childCount) return;
          moveBlock(editor, hover.index, to);
          requestAnimationFrame(() =>
            place(up ? hover.index - 1 : hover.index + 1),
          );
        }}
      >
        <GripVertical size={14} aria-hidden="true" />
      </button>
      {drop && (
        <div
          className="tilasm-drop-line"
          style={{ top: drop.top }}
          aria-hidden="true"
        />
      )}
    </>
  );
}

// SelectionBar is the bar near a selection: its marks, a link, and its block. A click keeps the selection; the link
// field keeps it too while it is typed in.
function SelectionBar({
  editor,
  scroller,
}: {
  editor: Editor;
  scroller: HTMLElement | Window;
}) {
  const [link, setLink] = useState<string | null>(null);
  const [blocks, setBlocks] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current.isActive("bold"),
      italic: current.isActive("italic"),
      strike: current.isActive("strike"),
      code: current.isActive("code"),
      link: current.isActive("link"),
      href: (current.getAttributes("link").href as string | undefined) ?? "",
      block: currentBlock(current),
    }),
  });
  const items = useMemo(blockItems, []);
  useEffect(() => {
    if (link !== null) field.current?.focus();
  }, [link]);
  // Escape folds the menu of blocks, the selection kept.
  useEffect(() => {
    if (!blocks) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setBlocks(false);
    };
    document.addEventListener("keydown", escape, true);
    return () => document.removeEventListener("keydown", escape, true);
  }, [blocks]);
  const options = useMemo(
    () => ({
      strategy: "fixed" as const,
      placement: "top" as const,
      offset: 8,
      flip: { padding: 8 },
      shift: { padding: 8 },
      scrollTarget: scroller,
      onHide: () => {
        setLink(null);
        setBlocks(false);
      },
    }),
    [scroller],
  );
  const applyLink = () => {
    const href = (link ?? "").trim();
    const chain = editor.chain().focus().extendMarkRange("link");
    if (href) chain.setLink({ href }).run();
    else chain.unsetLink().run();
    setLink(null);
  };
  const mark = (
    name: "bold" | "italic" | "strike" | "code",
    label: string,
    Icon: typeof Bold,
  ) => (
    <button
      type="button"
      className={`icon-button tilasm-bar-button${state[name] ? " is-active" : ""}`}
      aria-pressed={state[name]}
      title={label}
      aria-label={label}
      onClick={() => {
        const chain = editor.chain().focus();
        if (name === "bold") chain.toggleBold();
        if (name === "italic") chain.toggleItalic();
        if (name === "strike") chain.toggleStrike();
        if (name === "code") chain.toggleCode();
        chain.run();
      }}
    >
      <Icon size={14} aria-hidden="true" />
    </button>
  );
  const current = items.find((item) => item.id === state.block) ?? items[0]!;
  return (
    <BubbleMenu
      editor={editor}
      className="tilasm-bar"
      role="toolbar"
      aria-label={t("tilasms.formatting")}
      options={options}
      // On the page's body: a panel's transform or overflow would move or clip a fixed bar.
      appendTo={() => document.body}
      updateDelay={60}
      shouldShow={({ editor: at, view, state: now, element }) => {
        if (!at.isEditable || at.isActive("codeBlock")) return false;
        if (!view.hasFocus() && !element.contains(document.activeElement))
          return false;
        const { selection } = now;
        if (selection.empty || selection instanceof NodeSelection) return false;
        return now.doc.textBetween(selection.from, selection.to).length > 0;
      }}
      // A press on the bar keeps the editor's selection, but the link field takes the focus to be typed in.
      onMouseDown={(event) => {
        if (!(event.target instanceof HTMLInputElement)) event.preventDefault();
      }}
    >
      {link !== null ? (
        <form
          className="tilasm-bar-link"
          onSubmit={(event) => {
            event.preventDefault();
            applyLink();
          }}
        >
          <input
            ref={field}
            type="url"
            value={link}
            placeholder={t("tilasms.link_placeholder")}
            aria-label={t("tilasms.format.link")}
            onChange={(event) => setLink(event.target.value)}
            onKeyDown={(event: ReactKeyboardEvent) => {
              if (event.key !== "Escape") return;
              event.preventDefault();
              setLink(null);
              editor.commands.focus();
            }}
          />
          {state.link && (
            <button
              type="button"
              className="icon-button tilasm-bar-button"
              title={t("tilasms.link_remove")}
              aria-label={t("tilasms.link_remove")}
              onClick={() => {
                editor
                  .chain()
                  .focus()
                  .extendMarkRange("link")
                  .unsetLink()
                  .run();
                setLink(null);
              }}
            >
              <Unlink size={14} aria-hidden="true" />
            </button>
          )}
        </form>
      ) : (
        <>
          <span className="tilasm-bar-group">
            <button
              type="button"
              className="text-button tilasm-bar-block"
              aria-haspopup="menu"
              aria-expanded={blocks}
              title={t("tilasms.turn_into")}
              onClick={() => setBlocks(!blocks)}
            >
              {current.label}
              <ChevronDown size={12} aria-hidden="true" />
            </button>
          </span>
          <span className="tilasm-bar-group">
            {mark("bold", t("tilasms.format.bold"), Bold)}
            {mark("italic", t("tilasms.format.italic"), Italic)}
            {mark("strike", t("tilasms.format.strike"), Strikethrough)}
            {mark("code", t("tilasms.format.code"), Code)}
            <button
              type="button"
              className={`icon-button tilasm-bar-button${state.link ? " is-active" : ""}`}
              aria-pressed={state.link}
              title={t("tilasms.format.link")}
              aria-label={t("tilasms.format.link")}
              onClick={() => {
                setBlocks(false);
                setLink(state.href);
              }}
            >
              <Link size={14} aria-hidden="true" />
            </button>
          </span>
          {blocks && (
            <div
              className="tilasm-menu tilasm-bar-menu"
              role="menu"
              aria-label={t("tilasms.turn_into")}
            >
              {items
                .filter(
                  (item) => item.id !== "horizontalRule" && item.id !== "table",
                )
                .map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    role="menuitemradio"
                    aria-checked={item.id === state.block}
                    className={`tilasm-menu-item${item.id === state.block ? " is-active" : ""}`}
                    onClick={() => {
                      setBlocks(false);
                      turnInto(editor, item.id);
                    }}
                  >
                    {item.icon}
                    <span>{item.label}</span>
                  </button>
                ))}
            </div>
          )}
        </>
      )}
    </BubbleMenu>
  );
}

interface Slash {
  // The "/" and the query after it, in the document.
  from: number;
  to: number;
  query: string;
}

// slashAt finds a "/" command at the caret: at a block's start or after a space, never in code nor in a link.
function slashAt(editor: Editor): Slash | null {
  const { selection } = editor.state;
  if (!editor.isEditable || !selection.empty) return null;
  if (editor.isActive("codeBlock") || editor.isActive("code")) return null;
  if (editor.isActive("link")) return null;
  const { $from } = selection;
  if (!$from.parent.isTextblock) return null;
  const before = $from.parent.textBetween(0, $from.parentOffset, "", "￼");
  const found = /(?:^|\s)\/([^\s/]{0,24})$/u.exec(before);
  if (!found) return null;
  const length = found[1]!.length + 1;
  return {
    from: selection.from - length,
    to: selection.from,
    query: found[1]!,
  };
}

// SlashMenu is the menu of blocks a "/" opens by the caret, filtered as one types after it; the arrows, Enter and
// Escape drive it, and so does the mouse. Picking one replaces the command typed with the block.
function SlashMenu({ editor }: { editor: Editor }) {
  const [slash, setSlash] = useState<Slash | null>(null);
  const [index, setIndex] = useState(0);
  const [place, setPlace] = useState<{ left: number; top: number }>();
  // The "/" Escape closed: the menu stays closed until it is typed again.
  const dismissed = useRef(-1);
  const menu = useRef<HTMLDivElement>(null);
  const items = useMemo(blockItems, []);
  const found = useMemo(
    () => (slash ? matching(items, slash.query) : []),
    [items, slash],
  );
  const live = useRef({ slash, found, index });
  useLayoutEffect(() => {
    live.current = { slash, found, index };
  });

  const pick = useCallback(
    (item: BlockItem) => {
      const at = live.current.slash;
      if (!at) return;
      setSlash(null);
      run(
        editor.chain().focus().deleteRange({ from: at.from, to: at.to }),
        item.id,
      );
    },
    [editor],
  );

  useEffect(() => {
    const refresh = () => {
      const next = editor.isFocused ? slashAt(editor) : null;
      if (!next || next.from !== dismissed.current) dismissed.current = -1;
      const shown = next && next.from !== dismissed.current ? next : null;
      const before = live.current.slash;
      if (before?.from === shown?.from && before?.query === shown?.query)
        return;
      live.current = { ...live.current, slash: shown };
      setSlash(shown);
      setIndex(0);
    };
    const blur = () => setSlash(null);
    editor.on("transaction", refresh);
    editor.on("focus", refresh);
    editor.on("blur", blur);
    const keys = (event: KeyboardEvent) => {
      const { slash: open, found: list, index: at } = live.current;
      if (!open) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        dismissed.current = open.from;
        setSlash(null);
        return;
      }
      if (!list.length) return;
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        event.stopPropagation();
        const step = event.key === "ArrowDown" ? 1 : -1;
        setIndex((at + step + list.length) % list.length);
      } else if (event.key === "Enter" || event.key === "Tab") {
        event.preventDefault();
        event.stopPropagation();
        pick(list[at % list.length]!);
      }
    };
    const dom = editor.view.dom;
    dom.addEventListener("keydown", keys, { capture: true });
    return () => {
      editor.off("transaction", refresh);
      editor.off("focus", refresh);
      editor.off("blur", blur);
      dom.removeEventListener("keydown", keys, { capture: true });
    };
  }, [editor, pick]);

  // The menu by the caret, below it, or above it when the window ends first; inside the window's width.
  const position = useCallback(() => {
    const at = live.current.slash;
    if (!at || !menu.current) return;
    let caret: { left: number; top: number; bottom: number };
    try {
      caret = editor.view.coordsAtPos(at.from);
    } catch {
      return;
    }
    const { width, height } = menu.current.getBoundingClientRect();
    const below = caret.bottom + 6;
    const top =
      below + height > window.innerHeight - 8 && caret.top - height - 6 > 8
        ? caret.top - height - 6
        : below;
    const left = Math.max(
      8,
      Math.min(caret.left, window.innerWidth - width - 8),
    );
    setPlace((before) =>
      before?.left === left && before.top === top ? before : { left, top },
    );
  }, [editor]);
  useLayoutEffect(() => {
    if (slash) position();
    else setPlace(undefined);
  }, [slash, found.length, position]);
  useEffect(() => {
    if (!slash) return;
    window.addEventListener("scroll", position, true);
    window.addEventListener("resize", position);
    return () => {
      window.removeEventListener("scroll", position, true);
      window.removeEventListener("resize", position);
    };
  }, [slash, position]);
  useEffect(() => {
    menu.current
      ?.querySelector<HTMLElement>(`[data-index="${index}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [index]);

  if (!slash) return null;
  return createPortal(
    <div
      ref={menu}
      className="tilasm-menu tilasm-slash"
      role="listbox"
      aria-label={t("tilasms.blocks")}
      style={{
        left: place?.left ?? 0,
        top: place?.top ?? 0,
        visibility: place ? "visible" : "hidden",
      }}
      onMouseDown={(event) => event.preventDefault()}
    >
      {found.length === 0 ? (
        <p className="tilasm-menu-empty">{t("tilasms.blocks_none")}</p>
      ) : (
        found.map((item, at) => (
          <button
            key={item.id}
            type="button"
            role="option"
            aria-selected={at === index}
            data-index={at}
            className={`tilasm-menu-item${at === index ? " is-active" : ""}`}
            onMouseEnter={() => setIndex(at)}
            onClick={() => pick(item)}
          >
            {item.icon}
            <span>{item.label}</span>
          </button>
        ))
      )}
    </div>,
    document.body,
  );
}

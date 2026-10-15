// The editor of a Markdown tilasm (src/tilasms.tsx): Tiptap on the panel's surface, with what a document editor
// needs and no chrome of its own. A bar near the selection formats it, links it and changes its block; "/" at a block's
// start or after a space opens a menu of blocks by the caret; a handle in the left gutter moves a whole block, the
// drop shown by a line, in one step that undo takes back. The bar, the "/" menu, a block's menu and Cmd+J (Ctrl+J) ask
// the document agent for a change at the selection, the block or the caret. What it reads and writes is Markdown
// (src/tilasm-markdown.ts).
import { type Editor } from "@tiptap/core";
import { NodeSelection, PluginKey, TextSelection } from "@tiptap/pm/state";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import { BubbleMenu } from "@tiptap/react/menus";
import {
  Bold,
  ChevronDown,
  Code,
  CodeXml,
  Copy,
  GripVertical,
  Heading1,
  Heading2,
  Heading3,
  Italic,
  Link,
  List,
  ListOrdered,
  ListTodo,
  MessageSquarePlus,
  Minus,
  Pilcrow,
  Quote,
  Sparkles,
  SquareCode,
  Strikethrough,
  Table,
  Trash2,
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
import { HTML_LIVE } from "./tilasm-html";
import { htmlLiveView, openNextBlock } from "./tilasm-html-view";
import { tilasmExtensions } from "./tilasm-markdown";
import { platformShortcut } from "./tooltip";

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
        htmlView: htmlLiveView,
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
  | "table"
  | "htmlLive";

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
    {
      id: "htmlLive",
      label: t("tilasms.block.html"),
      words: "html interactive live chart widget embed simulator",
      icon: icon(CodeXml),
    },
  ];
}

// plain folds case and accents, so that a word typed without them finds a label written with them.
const plain = (text: string) =>
  text.normalize("NFD").replace(/\p{M}/gu, "").toLocaleLowerCase();

// The blocks a "/" query finds, by their label or their words.
function matching<T extends { label: string; words: string }>(
  items: T[],
  query: string,
): T[] {
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
  if (id === "htmlLive") chain.insertContent({ type: HTML_LIVE });
  chain.run();
}

// selected is the range the developer selected: the page's selection when it is in the text, which the editor may not
// have read yet right after a key moved it, else the editor's.
function selected(editor: Editor): { from: number; to: number } {
  const view = editor.view;
  const sel = (view.root as Document).getSelection?.();
  if (
    sel?.anchorNode &&
    sel.focusNode &&
    view.dom.contains(sel.anchorNode) &&
    view.dom.contains(sel.focusNode)
  ) {
    try {
      const anchor = view.posAtDOM(sel.anchorNode, sel.anchorOffset);
      const head = view.posAtDOM(sel.focusNode, sel.focusOffset);
      return { from: Math.min(anchor, head), to: Math.max(anchor, head) };
    } catch {
      // A node of the page the editor does not map: its own selection.
    }
  }
  const { from, to } = editor.state.selection;
  return { from, to };
}

// The bar's own key, to fold it once it opened a window.
const barKey = new PluginKey("tilasm-bar");

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
  onComment,
  onAsk,
  overlay,
}: {
  editor: Editor | null;
  editable: boolean;
  // Opens a comment on a range of the document: the bar's Comment, a block's.
  onComment?: (from: number, to: number) => void;
  // Opens a request to the document agent on a range, or at a place when from is to: the bar's, the "/" menu's, a
  // block's, Cmd+J's.
  onAsk?: (from: number, to: number) => void;
  // What stands over the text, in the editor's box: the discussions (src/tilasm-discussions.tsx).
  overlay?: (
    editor: Editor,
    box: RefObject<HTMLDivElement | null>,
  ) => ReactNode;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [scroller, setScroller] = useState<HTMLElement | Window>();
  useEffect(() => setScroller(scrollParent(box.current)), []);
  // Cmd+J (Ctrl+J) asks the agent at the selection, or at the caret.
  const asking = useRef(onAsk);
  useLayoutEffect(() => {
    asking.current = onAsk;
  });
  useEffect(() => {
    if (!editor || !editable) return;
    const dom = editor.view.dom;
    const keys = (event: KeyboardEvent) => {
      if (
        !asking.current ||
        !(event.metaKey || event.ctrlKey) ||
        event.shiftKey ||
        event.altKey ||
        event.key.toLowerCase() !== "j"
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      const { from, to } = selected(editor);
      asking.current(from, to);
    };
    dom.addEventListener("keydown", keys);
    return () => dom.removeEventListener("keydown", keys);
  }, [editor, editable]);
  return (
    <div className="tilasm-editor" ref={box}>
      <EditorContent editor={editor} />
      {editor && editable && scroller && (
        <>
          <SelectionBar
            editor={editor}
            scroller={scroller}
            onComment={onComment}
            onAsk={onAsk}
          />
          <SlashMenu editor={editor} onAsk={onAsk} />
          <BlockHandle
            editor={editor}
            box={box}
            onComment={onComment}
            onAsk={onAsk}
          />
        </>
      )}
      {editor && overlay?.(editor, box)}
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

// The blocks a block's menu turns a block into.
const turnable: BlockId[] = [
  "paragraph",
  "heading1",
  "heading2",
  "heading3",
  "bulletList",
  "orderedList",
  "taskList",
  "blockquote",
  "codeBlock",
];

// A block whose text the menu can turn into another block, or comment.
const textual = (name: string) =>
  !["horizontalRule", "table", "htmlLive"].includes(name);

// BlockMenu is the small menu a click on a block's handle opens by it: delete, duplicate, turn into, comment. The
// arrows move in it, Escape closes it and gives the handle its focus back; each action is one step of undo.
function BlockMenu({
  editor,
  index,
  top,
  onComment,
  onAsk,
  onClose,
}: {
  editor: Editor;
  index: number;
  top: number;
  onComment?: (from: number, to: number) => void;
  onAsk?: (from: number, to: number) => void;
  onClose: (refocus: boolean) => void;
}) {
  const menu = useRef<HTMLDivElement>(null);
  const items = useMemo(blockItems, []);
  const doc = editor.state.doc;
  const node = index < doc.childCount ? doc.child(index) : null;
  let from = 0;
  for (let i = 0; i < index && i < doc.childCount; i++)
    from += doc.child(i).nodeSize;
  const to = from + (node?.nodeSize ?? 0);
  const words = node ? textual(node.type.name) && node.textContent.trim() : "";
  useEffect(() => {
    menu.current?.querySelector<HTMLElement>("[role^='menuitem']")?.focus();
    const outside = (event: PointerEvent) => {
      if (!menu.current?.contains(event.target as Node)) onClose(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [onClose]);
  if (!node) return null;
  const current = (() => {
    const name = node.type.name;
    if (name === "heading") {
      const level = node.attrs.level as number;
      return level <= 3 ? (`heading${level}` as BlockId) : null;
    }
    const known: Record<string, BlockId> = {
      paragraph: "paragraph",
      bulletList: "bulletList",
      orderedList: "orderedList",
      taskList: "taskList",
      blockquote: "blockquote",
      codeBlock: "codeBlock",
    };
    return known[name] ?? null;
  })();
  // act runs an action on the block, then gives the text its focus at once (Tiptap's own focus waits for a frame): a
  // key pressed right after, undo, goes to the text.
  const act = (action: () => void, focus = true) => {
    action();
    if (focus) editor.view.focus();
    onClose(false);
  };
  const keys = (event: ReactKeyboardEvent) => {
    const all = [
      ...(menu.current?.querySelectorAll<HTMLElement>(
        "[role^='menuitem']:not(:disabled)",
      ) ?? []),
    ];
    const at = all.indexOf(document.activeElement as HTMLElement);
    let next: number;
    if (event.key === "ArrowDown") next = (at + 1) % all.length;
    else if (event.key === "ArrowUp") next = (at - 1 + all.length) % all.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = all.length - 1;
    else if (event.key === "Escape" || event.key === "Tab") {
      event.preventDefault();
      event.stopPropagation();
      onClose(event.key === "Escape");
      return;
    } else return;
    event.preventDefault();
    all[next]?.focus();
  };
  return (
    <div
      ref={menu}
      className="tilasm-menu tilasm-block-menu"
      role="menu"
      aria-label={t("tilasms.block_menu")}
      style={{ top }}
      onKeyDown={keys}
    >
      <button
        type="button"
        role="menuitem"
        className="tilasm-menu-item"
        onClick={() =>
          act(() => {
            editor.chain().deleteRange({ from, to }).scrollIntoView().run();
          })
        }
      >
        <Trash2 size={14} aria-hidden="true" />
        <span>{t("tilasms.block.delete")}</span>
      </button>
      <button
        type="button"
        role="menuitem"
        className="tilasm-menu-item"
        onClick={() =>
          act(() => {
            editor.view.dispatch(editor.state.tr.insert(to, node));
          })
        }
      >
        <Copy size={14} aria-hidden="true" />
        <span>{t("tilasms.block.duplicate")}</span>
      </button>
      {onComment && words && (
        <button
          type="button"
          role="menuitem"
          className="tilasm-menu-item"
          onClick={() => act(() => onComment(from, to), false)}
        >
          <MessageSquarePlus size={14} aria-hidden="true" />
          <span>{t("tilasms.comment")}</span>
        </button>
      )}
      {onAsk && words && (
        <button
          type="button"
          role="menuitem"
          className="tilasm-menu-item"
          onClick={() => act(() => onAsk(from, to), false)}
        >
          <Sparkles size={14} aria-hidden="true" />
          <span>{t("tilasms.ask.action")}</span>
        </button>
      )}
      {textual(node.type.name) && (
        <>
          <p className="tilasm-menu-label">{t("tilasms.turn_into")}</p>
          {items
            .filter((item) => turnable.includes(item.id))
            .map((item) => (
              <button
                key={item.id}
                type="button"
                role="menuitemradio"
                aria-checked={item.id === current}
                className={`tilasm-menu-item${item.id === current ? " is-active" : ""}`}
                onClick={() =>
                  act(() => {
                    const state = editor.state;
                    const $from = state.doc.resolve(from);
                    const $to = state.doc.resolve(to);
                    editor.view.dispatch(
                      state.tr.setSelection(TextSelection.between($from, $to)),
                    );
                    turnInto(editor, item.id);
                  })
                }
              >
                {item.icon}
                <span>{item.label}</span>
              </button>
            ))}
        </>
      )}
    </div>
  );
}

// BlockHandle is the handle by the block under the pointer, in the left gutter. Dragged, a line shows where the block
// lands; Escape cancels. Clicked, or Enter on it, it opens the block's menu. Focused, the arrows move its block up and
// down.
function BlockHandle({
  editor,
  box,
  onComment,
  onAsk,
}: {
  editor: Editor;
  box: RefObject<HTMLDivElement | null>;
  onComment?: (from: number, to: number) => void;
  onAsk?: (from: number, to: number) => void;
}) {
  // The block the handle stands by, and where the drag would drop it: a boundary between blocks.
  const [hover, setHover] = useState<{ index: number; top: number }>();
  const [drop, setDrop] = useState<{ boundary: number; top: number }>();
  const drag = useRef<{ index: number; pointer: number } | null>(null);
  const target = useRef<number | null>(null);
  // The block whose menu is open: the handle stays by it meanwhile.
  const [menu, setMenu] = useState<number | null>(null);
  const menuOpen = useRef(false);
  useLayoutEffect(() => {
    menuOpen.current = menu !== null;
  });
  // A press that dragged the block is not a click.
  const dragged = useRef(false);
  const handle = useRef<HTMLButtonElement>(null);
  const closeMenu = useCallback((refocus: boolean) => {
    setMenu(null);
    if (refocus) requestAnimationFrame(() => handle.current?.focus());
  }, []);
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
        dragged.current = true;
        setDrop({
          boundary,
          top: (after ? found.rect.bottom : found.rect.top) - bounds.top,
        });
        return;
      }
      if (
        menuOpen.current ||
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
      if (!drag.current && !menuOpen.current) setHover(undefined);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
    window.addEventListener("keydown", cancel, true);
    frame.addEventListener("pointerleave", leave);
    // The page's text moves under a still pointer: the handle follows its block.
    const follow = () => {
      if (!menuOpen.current) setHover(undefined);
    };
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
        ref={handle}
        type="button"
        className="tilasm-drag-handle"
        style={{ top: hover.top }}
        title={t("tilasms.move_block")}
        aria-label={t("tilasms.move_block")}
        aria-haspopup="menu"
        aria-expanded={menu !== null}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.preventDefault();
          dragged.current = false;
          drag.current = { index: hover.index, pointer: event.pointerId };
          document.body.classList.add("tilasm-dragging");
        }}
        onClick={() => {
          if (dragged.current) {
            dragged.current = false;
            return;
          }
          setMenu(menu === null ? hover.index : null);
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
      {menu !== null && (
        <BlockMenu
          editor={editor}
          index={menu}
          top={hover.top}
          onComment={onComment}
          onAsk={onAsk}
          onClose={closeMenu}
        />
      )}
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
  onComment,
  onAsk,
}: {
  editor: Editor;
  scroller: HTMLElement | Window;
  onComment?: (from: number, to: number) => void;
  onAsk?: (from: number, to: number) => void;
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
      pluginKey={barKey}
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
          {(onComment || onAsk) && (
            <span className="tilasm-bar-group">
              {onComment && (
                <button
                  type="button"
                  className="text-button tilasm-bar-block"
                  title={t("tilasms.comment")}
                  onClick={() => {
                    const { from, to } = selected(editor);
                    setBlocks(false);
                    editor.view.dispatch(
                      editor.state.tr.setMeta(barKey, "hide"),
                    );
                    onComment(from, to);
                  }}
                >
                  <MessageSquarePlus size={14} aria-hidden="true" />
                  {t("tilasms.comment")}
                </button>
              )}
              {onAsk && (
                <button
                  type="button"
                  className="text-button tilasm-bar-block"
                  title={t("tilasms.ask.hint", {
                    shortcut: platformShortcut("J").label,
                  })}
                  aria-keyshortcuts={platformShortcut("J").aria}
                  onClick={() => {
                    const { from, to } = selected(editor);
                    setBlocks(false);
                    editor.view.dispatch(
                      editor.state.tr.setMeta(barKey, "hide"),
                    );
                    onAsk(from, to);
                  }}
                >
                  <Sparkles size={14} aria-hidden="true" />
                  {t("tilasms.ask.action")}
                </button>
              )}
            </span>
          )}
          {blocks && (
            <div
              className="tilasm-menu tilasm-bar-menu"
              role="menu"
              aria-label={t("tilasms.turn_into")}
            >
              {items
                .filter(
                  (item) =>
                    item.id !== "horizontalRule" &&
                    item.id !== "table" &&
                    item.id !== "htmlLive",
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

// The "/" menu's entries: the blocks, and asking the agent at the caret.
type SlashItem =
  BlockItem | { id: "ask"; label: string; words: string; icon: ReactNode };

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
function SlashMenu({
  editor,
  onAsk,
}: {
  editor: Editor;
  onAsk?: (from: number, to: number) => void;
}) {
  const [slash, setSlash] = useState<Slash | null>(null);
  const [index, setIndex] = useState(0);
  const [place, setPlace] = useState<{ left: number; top: number }>();
  // The "/" Escape closed: the menu stays closed until it is typed again.
  const dismissed = useRef(-1);
  const menu = useRef<HTMLDivElement>(null);
  const asks = !!onAsk;
  const items = useMemo<SlashItem[]>(
    () => [
      ...blockItems(),
      ...(asks
        ? [
            {
              id: "ask" as const,
              label: t("tilasms.ask.action"),
              words: "ask agent ai write request",
              icon: <Sparkles size={14} aria-hidden="true" />,
            },
          ]
        : []),
    ],
    [asks],
  );
  const found = useMemo(
    () => (slash ? matching(items, slash.query) : []),
    [items, slash],
  );
  const asking = useRef(onAsk);
  useLayoutEffect(() => {
    asking.current = onAsk;
  });
  const live = useRef({ slash, found, index });
  useLayoutEffect(() => {
    live.current = { slash, found, index };
  });

  const pick = useCallback(
    (item: SlashItem) => {
      const at = live.current.slash;
      if (!at) return;
      setSlash(null);
      // Asking the agent takes the "/" away and opens the request at its place.
      if (item.id === "ask") {
        editor.chain().deleteRange({ from: at.from, to: at.to }).run();
        asking.current?.(at.from, at.from);
        return;
      }
      // A live HTML block opens on its source, to be written at once.
      if (item.id === "htmlLive") openNextBlock(editor);
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

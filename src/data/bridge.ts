// What crosses the sandbox between a tilasm's frame and the window (L08, step 3): the bridge Djinn appends to each of
// its pages (internal/plan/tilasm_bridge.js) tells the passage the developer selects and Ctrl+Alt+M; the window sends
// it the anchors to highlight. The frame is the tilasm's, which may say anything: the window takes a message only from
// its own frame (the caller checks event.source), and only of the shape and sizes below. At worst a forged one puts a
// false anchor on a draft the developer sees before writing a word.
import {
  CommentState,
  type TilasmComment,
} from "../../gen/ts/plan/v1/tilasm_pb";

// A passage of a tilasm's page: the quote, the 32 characters around it, its place in the page's text, and the heading
// above it. The fields of a TilasmAnchor on a passage.
export interface Passage {
  page: string;
  exact: string;
  prefix: string;
  suffix: string;
  start: number;
  end: number;
  label: string;
}

// Where the selection's last line stands in the frame's viewport, in CSS pixels.
export interface Place {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export type FromFrame =
  // The bridge runs in the page loaded: it waits for the anchors.
  | { type: "ready"; page: string }
  // The passage selected, or null once the selection is gone.
  | { type: "selection"; passage: Passage; at: Place }
  | { type: "selection"; passage: null }
  // Ctrl+Alt+M, pressed in the frame.
  | { type: "key"; name: "comment" };

// The limits of a TilasmAnchor's fields (api/plan/v1/tilasm.proto).
const MAX = { page: 4096, exact: 2000, context: 200, label: 1000 };

const text = (value: unknown, max: number): value is string =>
  typeof value === "string" && value.length <= max;
const place = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && Math.abs(value) < 1e6;
const offset = (value: unknown): value is number =>
  Number.isInteger(value) &&
  (value as number) >= 0 &&
  (value as number) <= 2 ** 31 - 1;

// fromFrame reads a message from the frame: what the bridge sends, or null for anything else.
export function fromFrame(data: unknown): FromFrame | null {
  if (typeof data !== "object" || data === null) return null;
  const m = data as Record<string, unknown>;
  if (m.djinn !== "tilasm") return null;
  switch (m.type) {
    case "ready":
      return text(m.page, MAX.page) ? { type: "ready", page: m.page } : null;
    case "key":
      return m.name === "comment" ? { type: "key", name: "comment" } : null;
    case "selection": {
      if (m.passage === null) return { type: "selection", passage: null };
      const passage = passageOf(m.passage);
      const at = m.at as Record<string, unknown> | null | undefined;
      if (
        !passage ||
        typeof at !== "object" ||
        at === null ||
        !place(at.left) ||
        !place(at.top) ||
        !place(at.right) ||
        !place(at.bottom)
      )
        return null;
      return {
        type: "selection",
        passage,
        at: { left: at.left, top: at.top, right: at.right, bottom: at.bottom },
      };
    }
  }
  return null;
}

function passageOf(value: unknown): Passage | null {
  if (typeof value !== "object" || value === null) return null;
  const p = value as Record<string, unknown>;
  if (
    !text(p.page, MAX.page) ||
    !text(p.exact, MAX.exact) ||
    p.exact.trim() === "" ||
    !text(p.prefix, MAX.context) ||
    !text(p.suffix, MAX.context) ||
    !text(p.label, MAX.label) ||
    !offset(p.start) ||
    !offset(p.end) ||
    p.end < p.start
  )
    return null;
  return {
    page: p.page,
    exact: p.exact,
    prefix: p.prefix,
    suffix: p.suffix,
    start: p.start,
    end: p.end,
    label: p.label,
  };
}

// An anchor the window sends the bridge to highlight: a draft's, a sent one's, or the passage of the card being
// written. Never a comment's text: the frame may have left for another page.
export interface Mark {
  id: string;
  state: "draft" | "sent" | "writing";
  page: string;
  exact: string;
  prefix: string;
  suffix: string;
  start: number;
  end: number;
}

// marksOf are the anchors to highlight in a tilasm's frame: its comments on a passage, and the passage being written.
export function marksOf(
  comments: readonly TilasmComment[],
  writing?: Passage,
): Mark[] {
  const marks: Mark[] = [];
  for (const c of comments) {
    const a = c.anchor;
    if (!a?.exact) continue;
    marks.push({
      id: c.id,
      state: c.state === CommentState.SENT ? "sent" : "draft",
      page: a.page || "index.html",
      exact: a.exact,
      prefix: a.prefix,
      suffix: a.suffix,
      start: a.start,
      end: a.end,
    });
  }
  if (writing)
    marks.push({
      id: "",
      state: "writing",
      page: writing.page,
      exact: writing.exact,
      prefix: writing.prefix,
      suffix: writing.suffix,
      start: writing.start,
      end: writing.end,
    });
  return marks;
}

// isCommentKey tells Ctrl+Alt+M, the shortcut that comments the selection (Google Docs' own), whatever the layout.
export function isCommentKey(event: {
  key: string;
  code: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}): boolean {
  return (
    event.ctrlKey &&
    event.altKey &&
    !event.shiftKey &&
    !event.metaKey &&
    (event.code === "KeyM" || event.key.toLowerCase() === "m")
  );
}

// What crosses the sandbox between a tilasm's frame and the window (L08, steps 3 to 5): the bridge Djinn appends to each
// of its pages (internal/plan/tilasm_bridge.js) tells the passage the developer selects and Ctrl+Alt+M, the element
// aimed at and Escape in Aim mode, and where each anchor stands in the page, found, changed or lost; the window sends
// it the anchors to highlight, Aim mode on or off, and the anchor to reveal. The frame is the tilasm's, which may say anything: the window takes a message only from
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

// An element of a tilasm's page, aimed at: its id, else its path from the body, with the start of its text, and what
// the developer saw. The fields of a TilasmAnchor on an element.
export interface ElementPick {
  page: string;
  elementId: string;
  path: string;
  fingerprint: string;
  label: string;
}

// What a new comment is on, picked in the frame: a passage or an element.
export type Pick = Passage | ElementPick;

export const isPassage = (pick: Pick): pick is Passage => "exact" in pick;

// How an anchor stands in the page the frame shows: as it was, changed, or gone.
export type PlaceStatus = "found" | "changed" | "lost";

// Where an anchor stands in the frame's viewport, when it shows; what it reads now, when it changed.
export interface Placed {
  id: string;
  status: PlaceStatus;
  top?: number;
  bottom?: number;
  now?: string;
}

export type FromFrame =
  // The bridge runs in the page loaded: it waits for the anchors.
  | { type: "ready"; page: string }
  // The passage selected, or null once the selection is gone.
  | { type: "selection"; passage: Passage; at: Place }
  | { type: "selection"; passage: null }
  // Ctrl+Alt+M, pressed in the frame; Escape, in Aim mode.
  | { type: "key"; name: "comment" | "escape" }
  // In Aim mode, the block under the mouse, null for none; then the one clicked, which ends Aim mode in the frame.
  | { type: "element"; hover: Place | null }
  | { type: "element"; element: ElementPick; at: Place }
  // The anchors sent, as found in the page of the version the window said, again as the page scrolls.
  | { type: "placed"; version: number; anchors: Placed[] };

// The limits of a TilasmAnchor's fields (api/plan/v1/tilasm.proto).
const MAX = {
  page: 4096,
  exact: 2000,
  context: 200,
  label: 1000,
  elementId: 1000,
  path: 2000,
  fingerprint: 2000,
  id: 100,
  places: 10000,
};
const STATUSES: readonly string[] = ["found", "changed", "lost"];

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
      return m.name === "comment" || m.name === "escape"
        ? { type: "key", name: m.name }
        : null;
    case "selection": {
      if (m.passage === null) return { type: "selection", passage: null };
      const passage = passageOf(m.passage);
      const at = boxOf(m.at);
      return passage && at ? { type: "selection", passage, at } : null;
    }
    case "element": {
      if (m.hover === null) return { type: "element", hover: null };
      if (m.hover !== undefined) {
        const hover = boxOf(m.hover);
        return hover ? { type: "element", hover } : null;
      }
      const element = elementOf(m.element);
      const at = boxOf(m.at);
      return element && at ? { type: "element", element, at } : null;
    }
    case "placed": {
      if (
        !offset(m.version) ||
        !Array.isArray(m.anchors) ||
        m.anchors.length > MAX.places
      )
        return null;
      const anchors: Placed[] = [];
      for (const a of m.anchors as unknown[]) {
        const p = placedOf(a);
        if (!p) return null;
        anchors.push(p);
      }
      return { type: "placed", version: m.version, anchors };
    }
  }
  return null;
}

function boxOf(value: unknown): Place | null {
  if (typeof value !== "object" || value === null) return null;
  const at = value as Record<string, unknown>;
  if (
    !place(at.left) ||
    !place(at.top) ||
    !place(at.right) ||
    !place(at.bottom)
  )
    return null;
  return { left: at.left, top: at.top, right: at.right, bottom: at.bottom };
}

function elementOf(value: unknown): ElementPick | null {
  if (typeof value !== "object" || value === null) return null;
  const e = value as Record<string, unknown>;
  if (
    !text(e.page, MAX.page) ||
    !text(e.elementId, MAX.elementId) ||
    !text(e.path, MAX.path) ||
    !text(e.fingerprint, MAX.fingerprint) ||
    !text(e.label, MAX.label) ||
    (e.path === "" && e.elementId === "")
  )
    return null;
  return {
    page: e.page,
    elementId: e.elementId,
    path: e.path,
    fingerprint: e.fingerprint,
    label: e.label,
  };
}

function placedOf(value: unknown): Placed | null {
  if (typeof value !== "object" || value === null) return null;
  const p = value as Record<string, unknown>;
  if (
    !text(p.id, MAX.id) ||
    typeof p.status !== "string" ||
    !STATUSES.includes(p.status)
  )
    return null;
  const out: Placed = { id: p.id, status: p.status as PlaceStatus };
  if (p.top !== undefined || p.bottom !== undefined) {
    if (!place(p.top) || !place(p.bottom)) return null;
    out.top = p.top;
    out.bottom = p.bottom;
  }
  if (p.now !== undefined) {
    if (!text(p.now, MAX.exact)) return null;
    out.now = p.now;
  }
  return out;
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

// An anchor the window sends the bridge to find, highlight and place: a draft's, a sent one's, or what the card being
// written is on. Never a comment's text: the frame may have left for another page.
export interface Mark {
  id: string;
  // The active card's anchor highlights stronger, as the one being written does.
  state: "draft" | "sent" | "writing" | "active";
  page: string;
  // A passage.
  exact: string;
  prefix: string;
  suffix: string;
  start: number;
  end: number;
  // An element.
  elementId: string;
  path: string;
  fingerprint: string;
}

// marksOf are the anchors to place in a tilasm's frame: its comments on a passage or an element, the active one
// stronger, and what the card being written is on.
export function marksOf(
  comments: readonly TilasmComment[],
  writing?: Pick,
  active = "",
): Mark[] {
  const marks: Mark[] = [];
  for (const c of comments) {
    const a = c.anchor;
    if (!a || !(a.exact || a.path || a.elementId)) continue;
    marks.push({
      id: c.id,
      state:
        c.id === active
          ? "active"
          : c.state === CommentState.SENT
            ? "sent"
            : "draft",
      page: a.page || "index.html",
      exact: a.exact,
      prefix: a.prefix,
      suffix: a.suffix,
      start: a.start,
      end: a.end,
      elementId: a.elementId,
      path: a.path,
      fingerprint: a.fingerprint,
    });
  }
  if (writing) marks.push(markOf(writing));
  return marks;
}

function markOf(pick: Pick): Mark {
  const none = {
    exact: "",
    prefix: "",
    suffix: "",
    start: 0,
    end: 0,
    elementId: "",
    path: "",
    fingerprint: "",
  };
  return isPassage(pick)
    ? {
        ...none,
        id: "",
        state: "writing",
        page: pick.page,
        exact: pick.exact,
        prefix: pick.prefix,
        suffix: pick.suffix,
        start: pick.start,
        end: pick.end,
      }
    : {
        ...none,
        id: "",
        state: "writing",
        page: pick.page,
        elementId: pick.elementId,
        path: pick.path,
        fingerprint: pick.fingerprint,
      };
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

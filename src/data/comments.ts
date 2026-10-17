// The developer's comments on a tilasm, as the Comments column beside its frame shows them (L08, flow A): drafts that
// reach no agent until they are sent. The column writes one on the whole tilasm, on a passage selected in the frame or
// on an element aimed at, edits a draft in place and deletes it with an Undo that stands five seconds. A new version of
// the tilasm places each anchor again: found, changed, or lost and kept apart, under Detached, until it is reattached.
// What the column holds between two renders is a reducer, tested with node, and so are the dots of the margin.
import {
  AnchorStatus,
  CommentState,
  type TilasmComment,
  type TilasmHold,
  TilasmHoldReason,
} from "../../gen/ts/plan/v1/tilasm_pb";
import type { Pick, PlaceStatus, Placed } from "./bridge";

// How long a deleted draft can be put back.
export const UNDO_MS = 5000;

// draftsOn are the drafts on a tilasm, in the order they were written: a new one comes below the others, an edited one
// keeps its place. Identifiers are UUIDv7, ordered by time; a draft put back by an Undo keeps its own.
export function draftsOn(
  drafts: readonly TilasmComment[],
  tilasmId: string,
): TilasmComment[] {
  return drafts
    .filter((c) => c.tilasmId === tilasmId && c.state === CommentState.DRAFT)
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// draftCounts counts the drafts of each tilasm, by its identifier: what the tab, the row and the head show.
export function draftCounts(
  drafts: readonly TilasmComment[],
): ReadonlyMap<string, number> {
  const counts = new Map<string, number>();
  for (const c of drafts)
    if (c.state === CommentState.DRAFT)
      counts.set(c.tilasmId, (counts.get(c.tilasmId) ?? 0) + 1);
  return counts;
}

// What a comment is on: the whole tilasm, a passage of it, or an element.
export type AnchorKind = "whole" | "passage" | "element";

export function anchorKind(comment: TilasmComment): AnchorKind {
  const a = comment.anchor;
  if (a?.exact) return "passage";
  if (a && (a.elementId || a.path || a.fingerprint)) return "element";
  return "whole";
}

// Column is what the column holds between two renders.
export interface Column {
  // The card being written: NEW for a new comment on the whole tilasm, a draft's identifier, or "" for none.
  editing: string;
  // Its text, as typed.
  text: string;
  // The passage or the element a new comment is on; none for the whole tilasm.
  anchor?: Pick;
  // The draft deleted last, while its Undo stands, until that time in milliseconds.
  undo?: { comment: TilasmComment; until: number };
}

// NEW is the card of a comment not stored yet.
export const NEW = "new";

export const emptyColumn: Column = { editing: "", text: "" };

export type ColumnAction =
  // "On the whole tilasm", Comment on a passage, or an element aimed at: a new card, below the others. A card being
  // written takes the passage or the element, and keeps its text.
  | { type: "write"; anchor?: Pick }
  // A draft opened for editing, with its text.
  | { type: "edit"; comment: TilasmComment }
  | { type: "type"; text: string }
  // Escape, or Cancel: the card goes back to what is stored.
  | { type: "cancel" }
  // The card was stored.
  | { type: "saved" }
  // A draft was deleted at now: its Undo stands until UNDO_MS later.
  | { type: "deleted"; comment: TilasmComment; now: number }
  // The Undo put the draft back, or was dismissed.
  | { type: "undone" }
  // The time passed: an Undo past its time goes.
  | { type: "tick"; now: number };

export function column(state: Column, action: ColumnAction): Column {
  switch (action.type) {
    case "write": {
      if (state.editing === NEW && state.anchor === action.anchor) return state;
      const next =
        state.editing === NEW ? state : { ...state, editing: NEW, text: "" };
      return action.anchor
        ? { ...next, anchor: action.anchor }
        : unanchored(next);
    }
    case "edit":
      if (state.editing === action.comment.id) return state;
      return {
        ...unanchored(state),
        editing: action.comment.id,
        text: action.comment.text,
      };
    case "type":
      return { ...state, text: action.text };
    case "cancel":
    case "saved":
      return { ...unanchored(state), editing: "", text: "" };
    case "deleted": {
      const editing = state.editing === action.comment.id;
      return {
        ...(editing ? { editing: "", text: "" } : state),
        undo: { comment: action.comment, until: action.now + UNDO_MS },
      };
    }
    case "undone":
      return state.undo ? { ...state, undo: undefined } : state;
    case "tick":
      return state.undo && state.undo.until <= action.now
        ? { ...state, undo: undefined }
        : state;
  }
}

// unanchored is a column whose card is on nothing in the page.
function unanchored(state: Column): Column {
  const { anchor: _, ...rest } = state;
  return rest;
}

// canSave tells whether the card being written can be stored: a comment says something.
export function canSave(state: Column): boolean {
  return state.editing !== "" && state.text.trim() !== "";
}

// SendFoot is what the foot of the column allows (decision Q29): a comment on the whole tilasm above two buttons,
// Launch and Iterate, that send every draft with it.
export interface SendFoot {
  // How many comments a send carries: the drafts, and the comment above the buttons once it says something.
  count: number;
  // Launch can go: without a comment, it answers the question that links the tilasm.
  launch: boolean;
  // Iterate can go: it needs a comment to say what to change.
  iterate: boolean;
  // The comments are greyed too, not only the buttons: a task works on the tilasm, or its work was launched. A send
  // that waits for the Djinn holds the buttons alone.
  greyed: boolean;
}

export function sendFoot(
  drafts: number,
  text: string,
  hold?: TilasmHold,
): SendFoot {
  const count = drafts + (text.trim() ? 1 : 0);
  const held =
    hold !== undefined && hold.reason !== TilasmHoldReason.UNSPECIFIED;
  return {
    count,
    launch: !held,
    iterate: !held && count > 0,
    greyed: held && hold.reason !== TilasmHoldReason.SENT,
  };
}

// detached tells a comment whose passage or element a later version lost: its card waits under Detached.
export const detached = (comment: TilasmComment): boolean =>
  comment.anchor?.status === AnchorStatus.LOST;

// columnGroups are the drafts as the column shows them, each group in the order written: those still on the tilasm,
// then the detached ones.
export function columnGroups(drafts: readonly TilasmComment[]): {
  attached: TilasmComment[];
  detached: TilasmComment[];
} {
  return {
    attached: drafts.filter((c) => !detached(c)),
    detached: drafts.filter(detached),
  };
}

const STATUS: Record<PlaceStatus, AnchorStatus> = {
  found: AnchorStatus.FOUND,
  changed: AnchorStatus.CHANGED,
  lost: AnchorStatus.LOST,
};

// placesToWrite are the statuses the window writes once the frame placed the anchors in a version
// (TilasmAnchor.status): for each comment written on an earlier version and not placed in this one yet. A comment on
// the version it was written on stands where it was written; one the frame did not place, on another page, waits.
export function placesToWrite(
  comments: readonly TilasmComment[],
  placed: readonly Placed[],
  version: number,
): { id: string; status: AnchorStatus }[] {
  const byId = new Map(placed.map((p) => [p.id, p]));
  const out: { id: string; status: AnchorStatus }[] = [];
  for (const c of comments) {
    const a = c.anchor;
    const p = byId.get(c.id);
    if (!a || !p || c.version >= version || a.statusVersion === version)
      continue;
    out.push({ id: c.id, status: STATUS[p.status] });
  }
  return out;
}

// A dot of the margin beside the frame: a comment's code, at the height of its anchor in the frame.
export interface Dot {
  id: string;
  code: string;
  top: number;
  sent: boolean;
}

// dotsOf are the dots of the margin: one per comment whose anchor shows in the frame, height pixels high, at the top
// of its anchor; two anchors on one line stack their dots, size pixels apart. Those past the frame's edges do not show.
export function dotsOf(
  comments: readonly TilasmComment[],
  placed: readonly Placed[],
  height: number,
  size: number,
): Dot[] {
  const codes = new Map(comments.map((c) => [c.id, c]));
  const dots: Dot[] = [];
  const shown = placed
    .filter((p) => p.top !== undefined && codes.has(p.id))
    .sort((a, b) => (a.top ?? 0) - (b.top ?? 0));
  let below = -Infinity;
  for (const p of shown) {
    const c = codes.get(p.id);
    if (!c || p.top === undefined || p.bottom === undefined) continue;
    if (p.bottom < 0 || p.top > height) continue;
    const top = Math.max(p.top, below, 0);
    if (top > height - size) continue;
    dots.push({
      id: c.id,
      code: c.code,
      top,
      sent: c.state === CommentState.SENT,
    });
    below = top + size;
  }
  return dots;
}

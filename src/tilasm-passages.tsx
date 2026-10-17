// Passages of a tilasm, commented where the developer reads them (L08, step 3). The bridge Djinn appends to the
// tilasm's pages (internal/plan/tilasm_bridge.js) tells the window, across the sandbox, the passage selected in the
// frame; a Comment button shows under it, over the frame, and Ctrl+Alt+M does the same, in the frame or out of it.
// The window sends the bridge the anchors of the drafts, and of the card being written, which it highlights in the
// page. What crosses is read by src/data/bridge.ts.
import { MessageSquarePlus } from "lucide-react";
import {
  type RefObject,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  type Mark,
  type Passage,
  type Place,
  fromFrame,
  isCommentKey,
} from "./data/bridge";
import { t } from "./i18n";

// The room the button takes, in CSS pixels, to keep it within the frame.
const BUTTON_WIDTH = 120;
const BUTTON_HEIGHT = 32;
const GAP = 6;

// usePassages listens to the bridge in frame while on: it returns the passage selected and where it ends in the
// frame, and sends the bridge marks to highlight, again when they change or the page loads again. onPick comments a
// passage: Ctrl+Alt+M calls it, as the Comment button does with pick.
export function usePassages(
  frame: RefObject<HTMLIFrameElement | null>,
  on: boolean,
  marks: readonly Mark[],
  onPick: (passage: Passage) => void,
): {
  selected: { passage: Passage; at: Place } | null;
  pick: () => void;
} {
  const [selected, setSelected] = useState<{
    passage: Passage;
    at: Place;
  } | null>(null);
  // The latest of each, for the listeners set once.
  const latest = useRef({ marks, onPick, selected });
  latest.current = { marks, onPick, selected };
  const post = (message: object) =>
    frame.current?.contentWindow?.postMessage(
      { ...message, djinn: "tilasm" },
      "*",
    );
  const pick = () => {
    const passage = latest.current.selected?.passage;
    if (!passage) return;
    setSelected(null);
    unselecting.current = true;
    post({ type: "unselect" });
    latest.current.onPick(passage);
  };
  const picking = useRef(pick);
  picking.current = pick;
  // Commented, until the bridge says the selection is gone: what it sent before is past.
  const unselecting = useRef(false);
  useEffect(() => {
    if (!on) {
      setSelected(null);
      return;
    }
    const onMessage = (event: MessageEvent) => {
      const source = frame.current?.contentWindow;
      if (!source || event.source !== source) return;
      const m = fromFrame(event.data);
      if (!m) return;
      if (m.type === "ready") {
        unselecting.current = false;
        setSelected(null);
        source.postMessage(
          { djinn: "tilasm", type: "anchors", anchors: latest.current.marks },
          "*",
        );
      } else if (unselecting.current) {
        if (m.type === "selection" && !m.passage) unselecting.current = false;
      } else if (m.type === "selection")
        setSelected(m.passage ? { passage: m.passage, at: m.at } : null);
      else picking.current();
    };
    // Out of the frame, Ctrl+Alt+M comments what is selected in it.
    const onKey = (event: KeyboardEvent) => {
      if (!isCommentKey(event) || !latest.current.selected) return;
      event.preventDefault();
      picking.current();
    };
    window.addEventListener("message", onMessage);
    document.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("message", onMessage);
      document.removeEventListener("keydown", onKey);
    };
  }, [on, frame]);
  // The marks, sent again only when they change.
  const key = on ? JSON.stringify(marks) : "";
  useEffect(() => {
    if (!key) return;
    frame.current?.contentWindow?.postMessage(
      { djinn: "tilasm", type: "anchors", anchors: JSON.parse(key) as Mark[] },
      "*",
    );
  }, [key, frame]);
  return { selected, pick };
}

// CommentPick is the Comment button under the passage selected in the frame, in the window, over the frame. It stays
// within the frame, and goes while the passage is out of its sight.
export function CommentPick({
  frame,
  at,
  onPick,
}: {
  frame: RefObject<HTMLIFrameElement | null>;
  // Where the passage's last line ends, in the frame's viewport.
  at: Place;
  onPick: () => void;
}) {
  const [spot, setSpot] = useState<{ left: number; top: number } | null>(null);
  // The frame moves with the page, or full size: the button follows it.
  const [moved, setMoved] = useState(0);
  useEffect(() => {
    const move = () => setMoved((n) => n + 1);
    window.addEventListener("scroll", move, { capture: true, passive: true });
    window.addEventListener("resize", move);
    return () => {
      window.removeEventListener("scroll", move, { capture: true });
      window.removeEventListener("resize", move);
    };
  }, []);
  useLayoutEffect(() => {
    const box = frame.current?.getBoundingClientRect();
    if (!box || at.bottom < 0 || at.top > box.height) return setSpot(null);
    setSpot({
      left: Math.max(
        box.left,
        Math.min(
          box.left + at.right - BUTTON_WIDTH / 2,
          box.right - BUTTON_WIDTH,
        ),
      ),
      top: Math.min(
        box.top + at.bottom + GAP,
        box.bottom - BUTTON_HEIGHT - GAP,
      ),
    });
  }, [frame, at, moved]);
  if (!spot) return null;
  return (
    <button
      type="button"
      className="button accent small tilasm-pick"
      style={{ left: spot.left, top: spot.top }}
      title={t("tilasms.comment_pick_title")}
      aria-keyshortcuts="Control+Alt+M"
      // The frame keeps its selection while the button is pressed.
      onMouseDown={(event) => event.preventDefault()}
      onClick={onPick}
    >
      <MessageSquarePlus size={13} aria-hidden="true" />
      {t("tilasms.comment_pick")}
    </button>
  );
}

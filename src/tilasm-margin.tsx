// Elements of a tilasm and the margin of its frame (L08, step 4). Aim mode comments an element the developer cannot
// select, a figure above all: the bridge Djinn appends to the tilasm's pages (internal/plan/tilasm_bridge.js) tells
// the block under the mouse, which the window frames over the frame, and the one clicked; Escape leaves, in the frame
// or out of it. The bridge also tells where each comment's anchor stands in the page, found, changed or lost (step 5), again
// as the page scrolls: numbered dots stand at those heights in a margin between the frame and the Comments column, and
// a click on one opens its card. What crosses is read by src/data/bridge.ts.
import {
  type RefObject,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import type { TilasmComment } from "../gen/ts/plan/v1/tilasm_pb";
import {
  type ElementPick,
  type Place,
  type Placed,
  fromFrame,
} from "./data/bridge";
import { dotsOf } from "./data/comments";
import { t } from "./i18n";

// useAim keeps the bridge in frame in Aim mode while aiming, and listens to it: it returns the block under the mouse,
// in the frame's viewport. A block clicked ends Aim mode and goes to onElement; Escape, in the frame or in the window,
// ends it too, and only it: full size stays. A page loaded again starts out of Aim mode.
export function useAim(
  frame: RefObject<HTMLIFrameElement | null>,
  on: boolean,
  aiming: boolean,
  setAiming: (aiming: boolean) => void,
  onElement: (element: ElementPick) => void,
): Place | null {
  const [hover, setHover] = useState<Place | null>(null);
  const latest = useRef({ setAiming, onElement });
  latest.current = { setAiming, onElement };
  const active = on && aiming;
  useEffect(() => {
    frame.current?.contentWindow?.postMessage(
      { djinn: "tilasm", type: "aim", on: active },
      "*",
    );
    if (!active) setHover(null);
  }, [active, frame]);
  useEffect(() => {
    if (!on) return;
    const onMessage = (event: MessageEvent) => {
      const source = frame.current?.contentWindow;
      if (!source || event.source !== source) return;
      const m = fromFrame(event.data);
      if (!m) return;
      if (m.type === "ready") latest.current.setAiming(false);
      else if (m.type === "key" && m.name === "escape")
        latest.current.setAiming(false);
      else if (m.type === "element") {
        if ("hover" in m) setHover(m.hover);
        else {
          latest.current.setAiming(false);
          latest.current.onElement(m.element);
        }
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [on, frame]);
  useEffect(() => {
    if (!active) return;
    // Before full size's own listener, on the document: Escape leaves Aim mode alone.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      latest.current.setAiming(false);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [active]);
  return active ? hover : null;
}

// AimBox frames the block under the mouse in Aim mode: in the window, over the frame, where the frame says.
export function AimBox({
  frame,
  at,
}: {
  frame: RefObject<HTMLIFrameElement | null>;
  at: Place;
}) {
  const [box, setBox] = useState<{
    left: number;
    top: number;
    width: number;
    height: number;
  } | null>(null);
  useLayoutEffect(() => {
    const view = frame.current?.getBoundingClientRect();
    if (!view) return setBox(null);
    // Within the frame: a block taller than it is framed where it shows.
    const left = Math.max(view.left, view.left + at.left - 3);
    const top = Math.max(view.top, view.top + at.top - 3);
    const right = Math.min(view.right, view.left + at.right + 3);
    const bottom = Math.min(view.bottom, view.top + at.bottom + 3);
    setBox(
      right > left && bottom > top
        ? { left, top, width: right - left, height: bottom - top }
        : null,
    );
  }, [frame, at]);
  if (!box) return null;
  return <div className="tilasm-aim-box" style={box} aria-hidden="true" />;
}

// usePlaced listens to where the bridge in frame placed the anchors, in the page of this version: what it last said,
// none once the page loads again. What the page of another version said is never taken for this one's, not even in
// the render where the version changes, before the frame loads the new page.
export function usePlaced(
  frame: RefObject<HTMLIFrameElement | null>,
  on: boolean,
  version: number,
): readonly Placed[] {
  const [placed, setPlaced] = useState<{
    version: number;
    anchors: readonly Placed[];
  }>({ version: 0, anchors: [] });
  useEffect(() => {
    if (!on) return;
    const onMessage = (event: MessageEvent) => {
      const source = frame.current?.contentWindow;
      if (!source || event.source !== source) return;
      const m = fromFrame(event.data);
      if (m?.type === "ready") setPlaced({ version: 0, anchors: [] });
      else if (m?.type === "placed" && m.version === version)
        setPlaced({ version, anchors: m.anchors });
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [on, frame, version]);
  return on && placed.version === version ? placed.anchors : none;
}

const none: readonly Placed[] = [];

// The height of a dot, and the room between two, in CSS pixels.
const DOT = 22;

// Margin is the strip between the frame and the Comments column: a numbered dot at the height of each anchor that
// shows in the frame, gold for a draft, blue once sent. A click on one opens its card.
export function Margin({
  comments,
  placed,
  active,
  onDot,
}: {
  comments: readonly TilasmComment[];
  placed: readonly Placed[];
  // The card open, whose dot stands out.
  active: string;
  onDot: (id: string) => void;
}) {
  const strip = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState(0);
  useLayoutEffect(() => {
    const el = strip.current;
    if (!el) return;
    setHeight(el.clientHeight);
    const observer = new ResizeObserver(() => setHeight(el.clientHeight));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  const dots = dotsOf(comments, placed, height, DOT);
  return (
    <div
      ref={strip}
      className="tilasm-margin"
      role="group"
      aria-label={t("tilasms.margin")}
    >
      {dots.map((dot) => (
        <button
          key={dot.id}
          type="button"
          className={`tilasm-dot${dot.sent ? " sent" : ""}${dot.id === active ? " active" : ""}`}
          style={{ top: dot.top }}
          title={t("tilasms.dot", { code: dot.code })}
          aria-label={t("tilasms.dot", { code: dot.code })}
          onClick={() => onDot(dot.id)}
        >
          {dot.code.slice(1)}
        </button>
      ))}
    </div>
  );
}

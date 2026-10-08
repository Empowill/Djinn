// The page keeps your place: when something above what you read grows, shrinks or goes away, the scroller moves
// by as much, so what you read stays where it is on screen. Browsers call it scroll anchoring (overflow-anchor).
// Chromium and Firefox have it. WebKit ships it switched off in WebKitGTK 2.50 (the window on Linux) and switched on
// only in recent sources; on macOS it depends on the system. Where the engine lacks it, and only there, this script
// does it, and the native one is turned off on the scroller.
//
// The anchor is the first element at the top of the view, as the CSS specification picks it. Its place is read in
// layout (offsetTop), not on screen: a transform or an animation of position never moves the page. A change is
// caught as the DOM changes (a microtask, before Motion measures its layout animations) and as sizes change (before
// paint). At the very top, nothing is anchored: what appears above shows.
import { type RefObject, useCallback } from "react";

// How many elements after the anchor, at each level, stand in for it when it goes away.
const SPARES = 3;

// layoutTop is the element's top in the page's layout, without scrolling nor transforms.
function layoutTop(el: HTMLElement): number {
  let top = 0;
  for (let e: Element | null = el; e instanceof HTMLElement; e = e.offsetParent)
    top += e.offsetTop;
  return top;
}

// inFlow tells an element that takes room in the scroller's layout: an element out of the flow (an exit animation
// pops one out) never anchors.
function inFlow(el: Element): el is HTMLElement {
  if (!(el instanceof HTMLElement) || !el.isConnected || !el.offsetParent)
    return false;
  const position = getComputedStyle(el).position;
  return (
    position !== "fixed" && position !== "sticky" && position !== "absolute"
  );
}

type Mark = { el: HTMLElement; top: number };

// pick returns the anchor, then its spares: the anchor is the deepest element that starts in the view at its top,
// or that crosses it with nothing inside to go deeper into. The spares are the elements after it, at its level and
// at each level above: when the anchor goes away with what holds it, an element after that still stands.
function pick(scroller: HTMLElement): Mark[] {
  if (scroller.scrollTop <= 0) return [];
  const viewTop = layoutTop(scroller) + scroller.clientTop + scroller.scrollTop;
  const levels: HTMLElement[][] = [];
  let level: Element = scroller;
  for (;;) {
    let found: HTMLElement | undefined;
    for (const child of level.children)
      if (inFlow(child) && layoutTop(child) + child.offsetHeight > viewTop) {
        found = child;
        break;
      }
    if (!found) break;
    const marks = [found];
    for (
      let next = found.nextElementSibling;
      next && marks.length <= SPARES;
      next = next.nextElementSibling
    )
      if (inFlow(next)) marks.push(next);
    levels.unshift(marks);
    if (layoutTop(found) >= viewTop || !found.children.length) break;
    level = found;
  }
  return levels.flat().map((el) => ({ el, top: layoutTop(el) }));
}

// keepPlace keeps the place of the reader in scroller until the returned function is called.
export function keepPlace(scroller: HTMLElement): () => void {
  if (CSS.supports("overflow-anchor", "auto")) return () => {};
  scroller.style.overflowAnchor = "none";
  // The anchors, and the scroll they were read at: the scroll is set from them, never added to, so rounding never
  // piles up.
  let marks: Mark[] = [];
  let scroll = 0;
  // The scroll this script set last: its own scroll event is not the reader's.
  let wrote = -1;
  const mark = () => {
    marks = pick(scroller);
    scroll = scroller.scrollTop;
  };
  const hold = () => {
    const anchor = marks.find((m) => inFlow(m.el));
    if (!anchor) return mark();
    const target = scroll + layoutTop(anchor.el) - anchor.top;
    if (Math.abs(scroller.scrollTop - target) >= 1) {
      scroller.scrollTop = target;
      wrote = scroller.scrollTop;
    }
  };
  let frame = 0;
  const onScroll = () => {
    if (Math.abs(scroller.scrollTop - wrote) < 1) return;
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(mark);
  };
  const mutations = new MutationObserver(hold);
  mutations.observe(scroller, {
    subtree: true,
    childList: true,
    attributes: true,
    characterData: true,
  });
  const sizes = new ResizeObserver(hold);
  const watch = () => {
    sizes.disconnect();
    for (const child of scroller.children) sizes.observe(child);
  };
  const children = new MutationObserver(watch);
  children.observe(scroller, { childList: true });
  watch();
  scroller.addEventListener("scroll", onScroll, { passive: true });
  mark();
  return () => {
    cancelAnimationFrame(frame);
    scroller.removeEventListener("scroll", onScroll);
    mutations.disconnect();
    children.disconnect();
    sizes.disconnect();
    scroller.style.overflowAnchor = "";
  };
}

// useKeepPlace gives a ref for a scroller that keeps the reader's place, and fills ref with the element.
export function useKeepPlace(ref: RefObject<HTMLDivElement | null>) {
  return useCallback(
    (el: HTMLDivElement | null) => {
      ref.current = el;
      if (!el) return;
      const stop = keepPlace(el);
      return () => {
        stop();
        ref.current = null;
      };
    },
    [ref],
  );
}

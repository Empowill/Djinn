// A view goes full size over the main shell, beside the side panel: a tilasm (tilasms.tsx), a task's diff (diff.tsx).
// What it lies over keeps its place and its scroll, out of reach of the keyboard, until the view leaves.
import { type RefObject, useEffect, useRef } from "react";

// useFullSize keeps the page under the full-size view out of reach of the keyboard and gives the focus to the button
// that leaves; Escape leaves too, unless a dialog takes it, or a field that took it for itself (a key pressed in a
// tilasm's frame stays in the frame's document). Once left, the focus goes back to the button that entered, if there
// is one. The page never scrolls for it.
export function useFullSize(
  full: boolean,
  view: RefObject<HTMLElement | null>,
  enter: RefObject<HTMLButtonElement | null>,
  leave: RefObject<HTMLButtonElement | null>,
  onLeave: () => void,
) {
  const was = useRef(false);
  // The last onLeave given: a new one at each render covers and uncovers nothing.
  const leaving = useRef(onLeave);
  leaving.current = onLeave;
  useEffect(() => {
    if (!full) {
      if (was.current) enter.current?.focus({ preventScroll: true });
      was.current = false;
      return;
    }
    was.current = true;
    const uncover = cover(view.current, leave.current);
    leave.current?.focus({ preventScroll: true });
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (
        event.target instanceof Element &&
        event.target.closest('dialog, [role="dialog"]')
      )
        return;
      event.preventDefault();
      leaving.current();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      uncover();
    };
  }, [full, view, enter, leave]);
}

// What stays in reach beside the full-size view, should it show after the view went full size: a tilasm's Comments
// column, its button, its margin of dots, and the Comment button over a passage.
const KEPT = ".tilasm-comments, .tilasm-pick, .tilasm-margin, [data-full-keep]";

// cover makes inert what the elements kept lie over in the main shell: every element there that holds none of them,
// and is none of KEPT. It returns what gives them back.
function cover(...keep: (HTMLElement | null)[]): () => void {
  const kept = keep.filter((el) => el !== null);
  const shell = kept[0]?.closest(".main-shell");
  const made: HTMLElement[] = [];
  for (let at = kept[0]?.parentElement; at && shell; at = at.parentElement) {
    for (const child of at.children)
      if (
        child instanceof HTMLElement &&
        !child.inert &&
        !child.matches(KEPT) &&
        !kept.some((el) => child.contains(el))
      ) {
        child.inert = true;
        made.push(child);
      }
    if (at === shell) break;
  }
  return () => made.forEach((el) => (el.inert = false));
}

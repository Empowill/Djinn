// The window's moves by the keyboard (W491), read on the window in wish-app.tsx: back and forward in the history of
// views (view-history.ts), as in Chrome, Alt+Left and Alt+Right, on macOS Cmd+[ and Cmd+], Cmd+Left and Cmd+Right too;
// the wish before or after in the side panel's order, Ctrl+Up and Ctrl+Down, the granted ones skipped. None takes a key
// from a text field, where the arrows with Alt, Cmd or Ctrl move the caret, nor from the terminal.
import { WishState, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { isActive } from "./data/format";
import { isMac } from "./tooltip";

type MoveKey = Pick<
  KeyboardEvent,
  "key" | "code" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey"
>;

// historyStep is where a key goes in the history of views: -1 back, 1 forward, 0 nowhere.
export function historyStep(event: MoveKey, mac = isMac()): number {
  if (event.shiftKey) return 0;
  const arrow =
    event.key === "ArrowLeft" ? -1 : event.key === "ArrowRight" ? 1 : 0;
  if (event.altKey && !event.ctrlKey && !event.metaKey) return arrow;
  if (!mac || !event.metaKey || event.ctrlKey || event.altKey) return 0;
  if (arrow) return arrow;
  if (event.key === "[" || event.code === "BracketLeft") return -1;
  if (event.key === "]" || event.code === "BracketRight") return 1;
  return 0;
}

// wishStep is where a key goes among the wishes: -1 the one above, 1 the one below, 0 nowhere. Ctrl on every system,
// and on macOS too, where Cmd with an arrow keeps its meaning (the top or the bottom of the page).
export function wishStep(event: MoveKey): number {
  if (!event.ctrlKey || event.altKey || event.metaKey || event.shiftKey)
    return 0;
  return event.key === "ArrowUp" ? -1 : event.key === "ArrowDown" ? 1 : 0;
}

// wishOrder is the side panel's order of the wishes Ctrl+Up and Ctrl+Down go through: the active ones by rank, then the
// paused ones; the granted ones are left out.
export function wishOrder(wishes: readonly Wish[]): string[] {
  return [
    ...wishes.filter(isActive),
    ...wishes.filter((w) => w.state === WishState.PAUSED),
  ].map((w) => w.id);
}

// nextWish is the wish a step goes to from the one shown, nothing past either end; from no wish of the list (a page, a
// granted wish), the first for a step down, the last for a step up.
export function nextWish(
  order: readonly string[],
  current: string,
  step: number,
): string | undefined {
  const at = order.indexOf(current);
  if (at < 0) return step > 0 ? order[0] : order.at(-1);
  return order[at + step];
}

// typingIn says whether a key goes to a text field or to the terminal: no window move takes it there.
export function typingIn(target: EventTarget | null): boolean {
  if (typeof HTMLElement === "undefined" || !(target instanceof HTMLElement))
    return false;
  return (
    target.isContentEditable ||
    target.matches("input, textarea, select") ||
    !!target.closest(".xterm")
  );
}

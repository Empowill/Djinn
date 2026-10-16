// The developer's presence (T51): a key, a click or a scroll in the window in front says they are there
// (UiService.Touch), at most once every 30 s. Djinn keeps their ifrits lit while they are.

export const TOUCH_EVERY = 30_000;

// The events that are the developer's own actions. A scroll does not bubble: it is caught on its way down.
const ACTIONS = ["keydown", "pointerdown", "wheel", "scroll"] as const;

export interface PresenceTarget {
  addEventListener(
    type: string,
    listener: () => void,
    options: AddEventListenerOptions,
  ): void;
  removeEventListener(
    type: string,
    listener: () => void,
    options: EventListenerOptions,
  ): void;
}

// followPresence calls touch on the developer's actions on target while focused says the window is in front, at
// most once every TOUCH_EVERY milliseconds; it returns what stops it.
export function followPresence(
  touch: () => Promise<unknown>,
  target: PresenceTarget,
  focused: () => boolean,
  now: () => number = Date.now,
): () => void {
  let last = -Infinity;
  const acted = () => {
    if (!focused()) return;
    const at = now();
    if (at - last < TOUCH_EVERY) return;
    last = at;
    void touch().catch(() => undefined);
  };
  for (const type of ACTIONS)
    target.addEventListener(type, acted, { capture: true, passive: true });
  return () => {
    for (const type of ACTIONS)
      target.removeEventListener(type, acted, { capture: true });
  };
}

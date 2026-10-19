// The window at rest: hidden, or not the window the developer works in. Its looping animations pause then
// (html[data-still] in styles.css), and what draws frames from script asks still() before drawing more: W503 measured
// the window's WebKit repainting a whole core while nobody looked at it. Only what moves for the eye asks it: data
// and counters stay live in a window beside the one in front (keepLive, W290).
export function still(): boolean {
  return document.hidden || !document.hasFocus();
}

// followStillness keeps <html data-still> true to still(); it returns how to stop.
export function followStillness(): () => void {
  const apply = () =>
    void document.documentElement.toggleAttribute("data-still", still());
  apply();
  window.addEventListener("focus", apply);
  window.addEventListener("blur", apply);
  document.addEventListener("visibilitychange", apply);
  return () => {
    window.removeEventListener("focus", apply);
    window.removeEventListener("blur", apply);
    document.removeEventListener("visibilitychange", apply);
  };
}

// onStillness calls back when the window comes to rest or wakes; it returns how to stop.
export function onStillness(callback: () => void): () => void {
  if (typeof MutationObserver === "undefined") return () => undefined;
  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-still"],
  });
  return () => observer.disconnect();
}

// How often a hidden or minimised window reads its data and moves its counters, at most.
export const UNSEEN_EVERY = 60_000;

// What keepLive reads of the page: whether it is out of sight, and when that changes.
export type Sight = Pick<
  Document,
  "hidden" | "addEventListener" | "removeEventListener"
>;

// keepLive calls tick every period while the window is in sight, focused or not: the developer watches Djinn with
// another window in front (W290). Hidden or minimised, it ticks every UNSEEN_EVERY at most, and at once as it shows
// again. It returns how to stop; the caller makes the first tick.
export function keepLive(
  tick: () => void,
  period: number,
  page: Sight = document,
): () => void {
  let id: ReturnType<typeof setInterval> | undefined;
  const start = () => {
    clearInterval(id);
    id = setInterval(
      tick,
      page.hidden ? Math.max(period, UNSEEN_EVERY) : period,
    );
  };
  const onVisibilityChange = () => {
    if (!page.hidden) tick();
    start();
  };
  start();
  page.addEventListener("visibilitychange", onVisibilityChange);
  return () => {
    clearInterval(id);
    page.removeEventListener("visibilitychange", onVisibilityChange);
  };
}

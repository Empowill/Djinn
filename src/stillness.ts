// The window at rest: hidden, or not the window the developer works in. Its looping animations pause then
// (html[data-still] in styles.css), and what draws frames from script asks still() before drawing more: W503 measured
// the window's WebKit repainting a whole core while nobody looked at it.
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

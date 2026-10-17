import { useSyncExternalStore } from "react";

// The window's text size, as an IDE offers it: the default, today's, or smaller steps that condense the interface to
// show more at once, or a larger one to read better. It is a preference of this page, kept in its storage, and set on
// <html data-text-size> before the first paint, like the theme (theme.ts). The stylesheets measure in rem, so a step
// scales the root font size (styles.css) and every length follows; what lays out in pixels from code reads textScale().
export type TextSize = "" | "large" | "compact" | "very-compact";

// The steps, from the largest; styles.css gives each its root font size.
export const textSizes: readonly TextSize[] = [
  "large",
  "",
  "compact",
  "very-compact",
];

const KEY = "djinn.text-size";

const known = (value: string): value is TextSize =>
  (textSizes as readonly string[]).includes(value);

export function chosenTextSize(): TextSize {
  try {
    const value = window.localStorage.getItem(KEY) ?? "";
    return known(value) ? value : "";
  } catch {
    return "";
  }
}

// applyTextSize sets the step on the page.
export function applyTextSize(size: TextSize = chosenTextSize()) {
  const html = document.documentElement;
  if (size) html.dataset.textSize = size;
  else delete html.dataset.textSize;
}

export function setTextSize(size: TextSize) {
  try {
    if (size) window.localStorage.setItem(KEY, size);
    else window.localStorage.removeItem(KEY);
  } catch {
    // Private window or blocked storage: the size lasts for this page only.
  }
  applyTextSize(size);
}

// shownTextSize is the step on the page now.
export function shownTextSize(): TextSize {
  if (typeof document === "undefined") return "";
  const value = document.documentElement.dataset.textSize ?? "";
  return known(value) ? value : "";
}

// textScale is how much the step scales a length of the default size, 16px to the rem: 1 by default.
export function textScale(): number {
  if (typeof getComputedStyle === "undefined") return 1;
  return (
    parseFloat(getComputedStyle(document.documentElement).fontSize) / 16 || 1
  );
}

// onTextSize calls back when the step on the page changes; it returns how to stop.
export function onTextSize(callback: () => void): () => void {
  if (typeof MutationObserver === "undefined") return () => undefined;
  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-text-size"],
  });
  return () => observer.disconnect();
}

// useTextScale renders again when the step changes, with its scale.
export const useTextScale = () =>
  useSyncExternalStore(onTextSize, textScale, () => 1);

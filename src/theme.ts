import { useSyncExternalStore } from "react";

// The window's theme: the system's by default; dark, as Clément drew it, or light when chosen in the settings. It is a
// preference of this page, kept in its storage, and set on <html data-theme> before the first paint. Every surface
// takes its colours from the tokens of theme.css; what draws outside CSS (the terminal, Mermaid) reads them here.
export type Theme = "" | "dark" | "light";

const KEY = "djinn.theme";

export function chosenTheme(): Theme {
  try {
    const value = window.localStorage.getItem(KEY) ?? "";
    // "system" was a choice before it became the default.
    return value === "dark" || value === "light" ? value : "";
  } catch {
    return "";
  }
}

const system = () =>
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-color-scheme: light)").matches;

// applyTheme sets the theme chosen on the page, and follows the system's while none is chosen.
export function applyTheme(theme: Theme = chosenTheme()) {
  const light = theme === "light" || (theme === "" && system());
  document.documentElement.dataset.theme = light ? "light" : "dark";
}

export function setTheme(theme: Theme) {
  try {
    if (theme) window.localStorage.setItem(KEY, theme);
    else window.localStorage.removeItem(KEY);
  } catch {
    // Private window or blocked storage: the theme lasts for this page only.
  }
  applyTheme(theme);
}

// followSystem applies the system's theme again when it changes.
export function followSystem() {
  if (typeof window.matchMedia !== "function") return;
  window
    .matchMedia("(prefers-color-scheme: light)")
    .addEventListener("change", () => applyTheme());
}

// shownTheme is the theme on the page now.
export const shownTheme = (): "dark" | "light" =>
  typeof document !== "undefined" &&
  document.documentElement.dataset.theme === "light"
    ? "light"
    : "dark";

// onTheme calls back when the theme on the page changes; it returns how to stop.
export function onTheme(callback: () => void): () => void {
  if (typeof MutationObserver === "undefined") return () => undefined;
  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => observer.disconnect();
}

// useShownTheme renders again when the theme changes.
export const useShownTheme = () =>
  useSyncExternalStore(onTheme, shownTheme, () => "dark" as const);

// token reads a colour of theme.css as the page has it now; empty outside a page.
export function token(name: string): string {
  if (typeof getComputedStyle === "undefined") return "";
  return getComputedStyle(document.documentElement)
    .getPropertyValue(`--${name}`)
    .trim();
}

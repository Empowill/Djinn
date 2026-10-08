// The window's theme: dark by default, as Clément drew it; light, or the system's, when chosen in the settings. It is
// a preference of this page, kept in its storage, and set on <html data-theme> before the first paint.
export type Theme = "" | "light" | "system";

const KEY = "djinn.theme";

export function chosenTheme(): Theme {
  try {
    const value = window.localStorage.getItem(KEY) ?? "";
    return value === "light" || value === "system" ? value : "";
  } catch {
    return "";
  }
}

const system = () =>
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-color-scheme: light)").matches;

// applyTheme sets the theme chosen on the page, and follows the system's while it is the one chosen.
export function applyTheme(theme: Theme = chosenTheme()) {
  const light = theme === "light" || (theme === "system" && system());
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

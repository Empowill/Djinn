// The waking splash inlined in index.html: shown from the first frame before the bundle and its reads, then removed
// once the interface has mounted and read its first data (or handed off to the in-app reading lines of W282). Never
// removed on a timer if the interface never mounted, so the silent-window watch of W354 still sees a stuck start.
import { t } from "./i18n";

export const SPLASH_ID = "djinn-splash";
export const SPLASH_STYLE_ID = "djinn-splash-style";
export const SPLASH_TEXT_ID = "djinn-splash-text";

// syncSplash sets the waking line to the chosen language as soon as the bundle starts, while #djinn-splash still covers
// the viewport.
export function syncSplash(root: Document = document): void {
  const line = root.getElementById(SPLASH_TEXT_ID);
  if (line) line.textContent = t("app.waking");
}

// hideSplash removes the splash and its style sheet from the document so its composited loops leave no element behind.
export function hideSplash(root: Document = document): void {
  root.getElementById(SPLASH_ID)?.remove();
  root.getElementById(SPLASH_STYLE_ID)?.remove();
}

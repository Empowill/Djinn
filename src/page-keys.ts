// The window's pages by the keyboard (Decision Q254): Ctrl+1 (⌘1 on macOS) shows the flying carpet, Ctrl+2 the
// Hourglass. A shortcut is the key's place, never its character: Ctrl+1 on QWERTY is Ctrl+& on AZERTY (Ctrl+2 the key
// of its e acute), and the numpad's digits work too. Digits 3 to 9 wait for pages to come.
import { useSyncExternalStore } from "react";

import { isMac, platformShortcut, type PlatformShortcut } from "./tooltip";

export const CARPET_DIGIT = 1;
export const HOURGLASS_DIGIT = 2;

type DigitEvent = Pick<
  KeyboardEvent,
  "code" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey"
>;

// pageDigit gives the digit, 1 to 9, a keydown asks a page by, else 0: the platform's modifier alone (⌘ on a Mac, Ctrl
// elsewhere) with a key of the digit row or of the numpad. With Shift or Alt (AltGr is Ctrl+Alt on Windows), none.
export function pageDigit(event: DigitEvent, mac = isMac()): number {
  const modifier = mac
    ? event.metaKey && !event.ctrlKey
    : event.ctrlKey && !event.metaKey;
  if (!modifier || event.altKey || event.shiftKey) return 0;
  const digit = /^(?:Digit|Numpad)([1-9])$/.exec(event.code);
  return digit ? Number(digit[1]) : 0;
}

// What each key of the digit row types here, by its code ("Digit1": "&" on AZERTY): from navigator.keyboard's layout
// map where the browser has one (Chromium), else learned from the keys typed (WebKit, Firefox), kept for the next
// start. A key not known yet shows its digit.
const LAYOUT = "djinn.digit-row";
type Layout = Readonly<Record<string, string>>;
let layout: Layout = readLayout();
const listeners = new Set<() => void>();

function readLayout(): Layout {
  try {
    const value: unknown = JSON.parse(
      globalThis.localStorage?.getItem(LAYOUT) ?? "{}",
    );
    return value && typeof value === "object" ? (value as Layout) : {};
  } catch {
    return {};
  }
}

function setLayout(keys: Record<string, string>) {
  const next = { ...layout, ...keys };
  if (Object.keys(next).every((code) => next[code] === layout[code])) return;
  layout = next;
  try {
    globalThis.localStorage?.setItem(LAYOUT, JSON.stringify(layout));
  } catch {
    // Private window or blocked storage: the layout is learned again at the next start.
  }
  for (const listener of listeners) listener();
}

type LearnEvent = DigitEvent &
  Pick<KeyboardEvent, "key"> & {
    getModifierState?: (key: string) => boolean;
  };

// learnDigitKey reads what a key of the digit row typed: its own character, so not with Shift, AltGr or Caps Lock (they
// type the digit on AZERTY, or another sign).
export function learnDigitKey(event: LearnEvent) {
  if (
    !/^Digit[1-9]$/.test(event.code) ||
    event.shiftKey ||
    event.altKey ||
    event.getModifierState?.("AltGraph") ||
    event.getModifierState?.("CapsLock") ||
    [...event.key].length !== 1
  )
    return;
  setLayout({ [event.code]: event.key.toLowerCase() });
}

type LayoutMap = { get(code: string): string | undefined };
type KeyboardApi = { getLayoutMap?: () => Promise<LayoutMap> };

let watching = false;

// watch starts reading the layout, once: the layout map asked, the keys typed anywhere in the window (in the terminal
// too, which keeps its keys from the window's own shortcuts: the capture comes first).
function watch() {
  if (watching || typeof window === "undefined") return;
  watching = true;
  window.addEventListener("keydown", learnDigitKey, true);
  const keyboard = (navigator as unknown as { keyboard?: KeyboardApi })
    .keyboard;
  keyboard
    ?.getLayoutMap?.()
    .then((map) => {
      const keys: Record<string, string> = {};
      for (let digit = 1; digit <= 9; digit++) {
        const key = map.get(`Digit${digit}`);
        if (key && [...key].length === 1)
          keys[`Digit${digit}`] = key.toLowerCase();
      }
      setLayout(keys);
    })
    // Refused in a frame, or by the browser's policy: the keys typed tell.
    .catch(() => undefined);
}

function subscribe(listener: () => void) {
  watch();
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

const snapshot = () => layout;

// digitKey is what the key of a digit types on this layout, the digit itself until it is known.
export function digitKey(digit: number, keys: Layout = layout): string {
  return keys[`Digit${digit}`] ?? String(digit);
}

// pageShortcut names a page's shortcut for this platform and layout: "Ctrl 1", "Ctrl &" on AZERTY, "⌘ 1" on a Mac.
export function pageShortcut(
  digit: number,
  keys: Layout = layout,
): PlatformShortcut {
  return platformShortcut(String(digit), digitKey(digit, keys));
}

// usePageShortcut is pageShortcut, named again once the layout is known.
export function usePageShortcut(digit: number): PlatformShortcut {
  return pageShortcut(
    digit,
    useSyncExternalStore(subscribe, snapshot, snapshot),
  );
}

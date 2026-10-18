// The page shortcuts (src/page-keys.ts) and the header actions' tooltips (src/tooltip.tsx, src/page-header.tsx). Run
// with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h } = await screens();

// A shortcut reads ⌘ on a Mac and Ctrl elsewhere, in the tooltip and in aria-keyshortcuts; an icon action of a page
// header names itself by its label and keeps its explanation for the tooltip, not a native title.
test("shortcuts follow the platform, and header actions carry tooltips, not titles", () => {
  globalThis.__DJINN_MAC__ = true;
  try {
    assert.deepEqual(s.platformShortcut("N"), {
      label: "⌘ N",
      aria: "Meta+N",
    });
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  globalThis.__DJINN_MAC__ = false;
  try {
    assert.deepEqual(s.platformShortcut("\\"), {
      label: "Ctrl \\",
      aria: "Control+\\",
    });
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  const action = s.renderToStaticMarkup(
    h(s.PageHeaderAction, { label: "Share", title: "Export the wish" }),
  );
  assert.match(action, /aria-label="Share"/);
  assert.doesNotMatch(action, /title=/);
  // Closed, the tooltip is not in the page.
  assert.doesNotMatch(action, /role="tooltip"/);
});

// Ctrl+1 and Ctrl+2 (Q254) are the key's place: the digit row, whatever it types (& and e acute on AZERTY), or the numpad;
// the platform's modifier alone, never with Shift or Alt.
test("a page's shortcut is the place of its digit key, on any layout, the numpad too", () => {
  const press = (code, key, more = {}) => ({
    code,
    key,
    ctrlKey: true,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    ...more,
  });
  // AZERTY: Ctrl+& is Digit1, the flying carpet; Ctrl+(e acute) is Digit2, the Hourglass.
  assert.equal(s.pageDigit(press("Digit1", "&"), false), 1);
  assert.equal(s.pageDigit(press("Digit2", "\u00e9"), false), 2);
  assert.equal(s.pageDigit(press("Digit1", "1"), false), 1);
  assert.equal(s.pageDigit(press("Numpad2", "2"), false), 2);
  // NumLock off, the numpad's 1 is End: still its place.
  assert.equal(s.pageDigit(press("Numpad1", "End"), false), 1);
  // Shift, Alt (AltGr is Ctrl+Alt on Windows), Ctrl with Meta: nothing.
  assert.equal(s.pageDigit(press("Digit1", "1", { shiftKey: true }), false), 0);
  assert.equal(s.pageDigit(press("Digit2", "~", { altKey: true }), false), 0);
  assert.equal(s.pageDigit(press("Digit1", "&", { metaKey: true }), false), 0);
  // No modifier, another key, the digit 0: nothing.
  assert.equal(s.pageDigit(press("Digit1", "&", { ctrlKey: false }), false), 0);
  assert.equal(s.pageDigit(press("KeyA", "1"), false), 0);
  assert.equal(s.pageDigit(press("Digit0", "\u00e0"), false), 0);
  // On a Mac, ⌘ and not Ctrl.
  const cmd = { ctrlKey: false, metaKey: true };
  assert.equal(s.pageDigit(press("Digit2", "\u00e9", cmd), true), 2);
  assert.equal(s.pageDigit(press("Digit2", "\u00e9"), true), 0);
});

// The tooltips name the shortcut as the keyboard writes it: the digit until the layout is known, its character then.
test("a page's shortcut is named for the keyboard's layout once it is known", () => {
  globalThis.__DJINN_MAC__ = false;
  try {
    assert.deepEqual(s.pageShortcut(1, {}), {
      label: "Ctrl 1",
      aria: "Control+1",
    });
    const azerty = { Digit1: "&", Digit2: "\u00e9" };
    assert.equal(s.pageShortcut(1, azerty).label, "Ctrl &");
    assert.equal(s.pageShortcut(2, azerty).label, "Ctrl \u00e9");
    assert.equal(s.pageShortcut(2, azerty).aria, "Control+2");
    assert.equal(s.digitKey(3, azerty), "3");
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  globalThis.__DJINN_MAC__ = true;
  try {
    assert.equal(s.pageShortcut(2, { Digit2: "\u00e9" }).label, "⌘ \u00e9");
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  // A key typed teaches its character; with Shift, Caps Lock or AltGr it types another, and teaches nothing.
  const typed = (code, key, more = {}) => ({
    code,
    key,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    getModifierState: () => false,
    ...more,
  });
  s.learnDigitKey(typed("Digit4", "1", { shiftKey: true }));
  s.learnDigitKey(
    typed("Digit4", "4", { getModifierState: (m) => m === "CapsLock" }),
  );
  s.learnDigitKey(typed("Digit4", "{", { altKey: true }));
  s.learnDigitKey(typed("Numpad4", "4"));
  assert.equal(s.digitKey(4), "4");
  s.learnDigitKey(typed("Digit4", "'"));
  assert.equal(s.digitKey(4), "'");
  s.learnDigitKey(typed("Digit4", "4", { ctrlKey: true }));
  assert.equal(s.digitKey(4), "4");
});

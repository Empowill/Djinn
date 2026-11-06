// The free-text fields (src/text-field.tsx): their text is the browser's own, the owner renders only when what it
// shows around the text changes (W526). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";
import { screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();

const below = (el, out = []) => {
  for (const child of el.childNodes) {
    out.push(child);
    below(child, out);
  }
  return out;
};
const props = (el) =>
  el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
const textOf = (el) =>
  below(el)
    .filter((n) => n.nodeType === 3)
    .map((n) => n.nodeValue ?? n.textContent ?? n.data)
    .join("");

// mount renders an owner that holds its text in useTyped, and counts its renders.
async function mount(field, extra = {}) {
  const container = setupMockDom();
  const seen = { renders: 0, typed: undefined };
  function Owner() {
    const typed = s.useTyped(s.hasText);
    seen.renders++;
    seen.typed = typed;
    return h(
      "div",
      null,
      h(field, { text: typed.text, onText: typed.type, ...extra }),
      h("button", { disabled: !typed.shown }, "Send"),
    );
  }
  const root = s.createRoot(container);
  await s.act(async () => root.render(h(Owner)));
  const tag = field === s.TextLine ? "INPUT" : "TEXTAREA";
  const el = below(container).find((n) => n.nodeName === tag);
  const type = (value) =>
    s.act(() => {
      el.value = value;
      props(el).onChange({ target: el });
    });
  const sendable = () =>
    !(
      "disabled" in
      below(container).find((n) => n.nodeName === "BUTTON").attributes
    );
  const end = async () => {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  };
  return { container, seen, el, type, sendable, end };
}

for (const [name, field] of [
  ["TextField", () => s.TextField],
  ["TextLine", () => s.TextLine],
])
  test(`${name}: a key renders its owner only when it starts or stops holding text; set writes the field`, async () => {
    const m = await mount(field());
    try {
      assert.equal(m.sendable(), false);
      await m.type("H");
      assert.equal(m.sendable(), true, "the first letter shows");
      const after = m.seen.renders;
      for (const value of ["He", "Hel", "Hell", "Hello"]) await m.type(value);
      assert.equal(m.seen.renders, after, "the keys after it render nothing");
      assert.equal(m.seen.typed.read(), "Hello", "read gives the text now");
      assert.equal(m.seen.typed.text, "H", "text is the last render's");
      await m.type("");
      assert.equal(m.sendable(), false, "emptied, it shows again");
      await m.type("Again");
      await s.act(() => m.seen.typed.set(""));
      assert.equal(m.el.value, "", "set writes the field");
      assert.equal(m.sendable(), false);
    } finally {
      await m.end();
    }
  });

test("TextField counts its text from 80 % of maxLength, warm at 95 %, and the count alone renders", async () => {
  const m = await mount(s.TextField, { maxLength: 20 });
  try {
    const count = () =>
      below(m.container).find((n) =>
        String(n.attributes?.class ?? "").startsWith("note-count"),
      );
    await m.type("x".repeat(15));
    assert.equal(count(), undefined, "under 80 %, no count");
    const renders = m.seen.renders;
    await m.type("x".repeat(16));
    assert.equal(textOf(count()), "16 / 20");
    assert.equal(count().attributes.class, "note-count");
    assert.ok("data-anchor-skip" in count().attributes);
    await m.type("x".repeat(19));
    assert.equal(textOf(count()), "19 / 20");
    assert.equal(count().attributes.class, "note-count near");
    assert.equal(
      m.seen.renders,
      renders,
      "the owner renders not for the count",
    );
    await s.act(() => m.seen.typed.set(""));
    assert.equal(count(), undefined, "cleared, the count goes");
  } finally {
    await m.end();
  }
});

test("keepPlace hears no change inside data-anchor-skip: a note's count moves no anchor", async () => {
  const observers = [];
  globalThis.CSS = { supports: () => false };
  globalThis.MutationObserver = class {
    constructor(callback) {
      this.callback = callback;
      observers.push(this);
    }
    observe() {}
    disconnect() {}
  };
  globalThis.ResizeObserver = class {
    observe() {}
    disconnect() {}
  };
  globalThis.Node = { ELEMENT_NODE: 1 };
  globalThis.requestAnimationFrame = () => 1;
  globalThis.cancelAnimationFrame = () => {};
  const { keepPlace } = await bundle(
    "scroll-anchor",
    `export { keepPlace } from "@/src/scroll-anchor.ts";`,
  );
  let reads = 0;
  const scroller = {
    style: {},
    children: [],
    addEventListener() {},
    removeEventListener() {},
    get scrollTop() {
      reads++;
      return 0;
    },
  };
  const stop = keepPlace(scroller);
  const mutations = observers[0];
  const inCount = { nodeType: 3, parentElement: { closest: () => ({}) } };
  const elsewhere = { nodeType: 1, closest: () => null };
  reads = 0;
  mutations.callback([{ target: inCount }]);
  assert.equal(reads, 0, "the count's change is not heard");
  mutations.callback([{ target: inCount }, { target: elsewhere }]);
  assert.ok(reads > 0, "a change around it is");
  stop();
});

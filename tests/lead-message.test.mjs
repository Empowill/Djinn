// Write to the Djinn (src/lead-message.tsx): the pill's box. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();

// W472: under load each render and layout of a key cost 3 to 5 times their time (L50), and the letters came in bursts.
// A key now goes to the draft and renders nothing: only the first letter, which enables the send button, does.
test("a key typed in the box renders nothing; Enter sends the last text typed and clears the box", async () => {
  const container = setupMockDom();
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const byLabel = (label) =>
    below(container).find((el) => el.attributes?.["aria-label"] === label);
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };
  const told = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.LeadService, {
      tell: (req) => {
        told.push([req.text, req.wishId]);
        return { waiting: false };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  let renders = 0;
  const root = s.createRoot(container);
  try {
    await s.act(async () =>
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(
            s.Profiler,
            { id: "pill", onRender: () => renders++ },
            h(s.LeadMessage, {
              wishId: "w1",
              stopped: false,
              onResume: async () => {},
            }),
          ),
        ),
      ),
    );
    await s.act(async () => props(byLabel("Write to the Djinn")).onClick());
    const box = below(container).find((el) => el.nodeName === "TEXTAREA");
    assert.ok(box, "the pill opens into its box");
    const sendable = () =>
      !("disabled" in byLabel("Send to the Djinn").attributes);
    assert.equal(sendable(), false, "nothing to send yet");
    // Typed as the browser does: the box holds the text, then React hears the change.
    const type = (value) =>
      s.act(() => {
        box.value = value;
        props(box).onChange({ target: box });
      });
    await type("H");
    assert.equal(sendable(), true, "the first letter enables Send");
    const before = renders;
    for (const value of ["He", "Hel", "Hell", "Hello", "Hello\nlead"])
      await type(value);
    await settle();
    assert.equal(renders, before, "the keys after the first render nothing");
    // Enter sends what the box holds, though nothing rendered it.
    await s.act(async () =>
      props(box).onKeyDown({
        key: "Enter",
        shiftKey: false,
        nativeEvent: { isComposing: false },
        preventDefault() {},
      }),
    );
    await settle();
    assert.deepEqual(told, [["Hello\nlead", "w1"]]);
    assert.equal(box.value, "", "sent, the box is cleared");
    assert.equal(sendable(), false);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

// T70: the bubble is an acknowledgement of one sentence; it fades 2 minutes after it shows, seen, so a reload does not
// bring it back. The clock is simulated: Date and setTimeout are node:test's mock timers.
test("the lead's bubble fades 2 minutes after it shows and does not come back", async (t) => {
  const container = setupMockDom();
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  // An element is never asserted equal: the message of a failure would print the whole mock tree, and hang.
  const bubbleText = () =>
    below(container).find((el) =>
      (el.attributes?.class ?? "").split(" ").includes("lead-bubble-text"),
    );
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setImmediate(r)));
  };
  const mem = new Map();
  const hadStorage = Object.getOwnPropertyDescriptor(
    globalThis,
    "localStorage",
  );
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (k) => mem.get(k) ?? null,
      setItem: (k, v) => mem.set(k, String(v)),
      removeItem: (k) => mem.delete(k),
    },
  });
  const now = 1_700_000_000_000;
  const said = {
    id: "b1",
    wishId: "w1",
    kind: "reply",
    content: "Got it, on it.",
    position: 1n,
    createTime: { seconds: BigInt(now / 1000 - 5), nanos: 0 },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.BlockService, { list: () => ({ blocks: [said], total: 1 }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open("w1");
  await djinn.store.changed("w1", [s.Change.BLOCK]);
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now });
  const pill = () =>
    h(
      s.DjinnProvider,
      { djinn },
      h(s.LeadMessage, {
        wishId: "w1",
        stopped: false,
        onResume: async () => {},
      }),
    );
  let root = s.createRoot(container);
  try {
    await s.act(async () => root.render(pill()));
    await settle();
    assert.equal(
      bubbleText()?.textContent,
      "Got it, on it.",
      "the lead's reply shows in the bubble",
    );

    await s.act(async () => t.mock.timers.tick(2 * 60 * 1000 - 1000));
    await settle();
    assert.ok(bubbleText(), "a second before its 2 minutes, it still shows");

    await s.act(async () => t.mock.timers.tick(1000));
    await settle();
    assert.ok(!bubbleText(), "at 2 minutes it fades");
    assert.deepEqual(JSON.parse(mem.get("djinn-lead-say-seen")), ["b1"]);

    // A reload reads it seen: the bubble stays empty.
    await s.act(async () => root.unmount());
    root = s.createRoot(container);
    await s.act(async () => root.render(pill()));
    await settle();
    assert.ok(!bubbleText(), "a faded bubble does not come back");
  } finally {
    s.act(() => root.unmount());
    t.mock.timers.reset();
    close();
    if (hadStorage)
      Object.defineProperty(globalThis, "localStorage", hadStorage);
    else delete globalThis.localStorage;
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

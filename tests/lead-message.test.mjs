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

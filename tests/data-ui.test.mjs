// The unified UI client (UiService.Watch) against an in-memory UiService. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { createUi, createRouterTransport, UiService, LoadNotch } = await bundle(
  "ui",
  `export { createUi } from "@/src/data/ui.ts";
export { createRouterTransport } from "@connectrpc/connect";
export { UiService } from "@/gen/ts/ui/v1/ui_pb.ts";
export { LoadNotch } from "@/gen/ts/djinn/v1/load_pb.ts";`,
);

const wishId = "01a11833-a440-7479-a067-52615c91da71";

test("a single watch stream shares show, update and load across subscribers", async () => {
  let watchCalls = 0;
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watch() {
        watchCalls++;
        yield {
          update: { current: "v1", ready: "", notResumed: [] },
        };
        yield {
          show: { wishId, terminal: `lead-${wishId}` },
        };
        yield {
          load: {
            notch: LoadNotch.HIGH,
            auto: false,
            chosenNotch: LoadNotch.HIGH,
          },
        };
        yield {
          update: {
            current: "v1",
            ready: "v2",
            notResumed: [],
            notesUrl: "https://example.com/v2",
          },
        };
        await new Promise(() => {}); // The stream stays open.
      },
    });
  });

  const ui = createUi(transport, 10);

  const focusSeen = [];
  ui.focus.subscribe((focus) => focusSeen.push(focus));

  const loadSeen = [];
  ui.load.subscribe((load) => loadSeen.push(load));

  const updateSeen = [];
  await new Promise((resolve) => {
    ui.update.subscribe((state) => {
      updateSeen.push(state.ready);
      if (state.ready === "v2") resolve();
    });
  });

  assert.equal(watchCalls, 1, "only 1 watch stream should be opened");
  assert.equal(focusSeen.length, 1);
  assert.deepEqual(focusSeen[0], {
    wishId,
    page: "",
    terminal: `lead-${wishId}`,
    tilasmId: "",
    target: "",
    unknownLink: "",
  });
  assert.equal(loadSeen.length, 1);
  assert.equal(loadSeen[0].notch, LoadNotch.HIGH);
  assert.deepEqual(updateSeen, ["", "v2"]);
});

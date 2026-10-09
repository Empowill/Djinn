// The update client against an in-memory UiService. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { createUpdate, createRouterTransport, UiService } = await bundle(
  "update",
  `export { createUpdate } from "@/src/data/update.ts";
export { createRouterTransport } from "@connectrpc/connect";
export { UiService } from "@/gen/ts/ui/v1/ui_pb.ts";`,
);

test("a newer Djinn is offered, and restarts only on update()", async () => {
  let updates = 0;
  let offer;
  const offered = new Promise((resolve) => (offer = resolve));
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchUpdate() {
        yield { current: "v1", ready: "", notResumed: [] };
        await offered;
        yield {
          current: "v1",
          ready: "v2",
          notResumed: [],
          notesUrl: "https://example.com/v2",
        };
        await new Promise(() => {}); // The stream stays open, as the server's does.
      },
      update: () => {
        updates++;
        return { version: "v2", terminals: 1 };
      },
    });
  });
  const djinnUpdate = createUpdate(transport, 10);
  const seen = [];
  await new Promise((resolve) =>
    djinnUpdate.subscribe((state) => {
      seen.push(state.ready);
      if (state.ready) resolve();
      else offer();
    }),
  );
  assert.deepEqual(seen, ["", "v2"]);
  assert.equal(updates, 0);
  // A part of the page that subscribes later gets the offer at once.
  const late = await new Promise((resolve) => djinnUpdate.subscribe(resolve));
  assert.equal(late.ready, "v2");
  assert.equal(late.notesUrl, "https://example.com/v2");
  assert.deepEqual(await djinnUpdate.update(), { version: "v2", terminals: 1 });
  assert.equal(updates, 1);
});

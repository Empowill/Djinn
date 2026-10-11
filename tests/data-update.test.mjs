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
  assert.deepEqual(await djinnUpdate.update(), {
    version: "v2",
    terminals: 1,
    installed: "",
  });
  assert.equal(updates, 1);
});

test("a build committed is proposed, and installs only on update(sha)", async () => {
  const asked = [];
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchUpdate() {
        yield {
          current: "v1",
          ready: "",
          notResumed: [],
          build: {
            wishTitle: "Run Djinn on itself",
            project: "djinn",
            branch: "feat/x",
            sha: "abc",
            tasks: ["W5"],
            changes: ["Work of W5"],
            summaries: [
              { code: "W5", title: "Work of W5", summary: "check the banner" },
            ],
          },
        };
        await new Promise(() => {});
      },
      update: (req) => {
        asked.push(req.build);
        return { version: "", terminals: 0, installed: req.build };
      },
    });
  });
  const djinnUpdate = createUpdate(transport, 10);
  const state = await new Promise((resolve) => djinnUpdate.subscribe(resolve));
  assert.deepEqual(state.build, {
    wishTitle: "Run Djinn on itself",
    project: "djinn",
    branch: "feat/x",
    sha: "abc",
    tasks: ["W5"],
    changes: ["Work of W5"],
    summaries: [
      { code: "W5", title: "Work of W5", summary: "check the banner" },
    ],
  });
  assert.deepEqual(asked, []);
  assert.deepEqual(await djinnUpdate.update("abc"), {
    version: "",
    terminals: 0,
    installed: "abc",
  });
  assert.deepEqual(asked, ["abc"]);
});

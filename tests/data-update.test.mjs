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
  // A start that resumed its workers holds none.
  assert.equal(late.held, undefined);
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
          builds: [
            {
              wishTitle: "Run Djinn on itself",
              project: "djinn",
              branch: "feat/x",
              sha: "abc",
              tasks: ["W5"],
              changes: ["Work of W5"],
              summaries: [
                {
                  code: "W5",
                  title: "Work of W5",
                  summary: "check the banner",
                },
              ],
              base: "0123",
              running: true,
              changesCount: 1,
              lacks: ["Work of W4"],
              lacksCount: 1,
              auto: true,
            },
            { branch: "feat/clement-port", sha: "e60" },
          ],
          readyBuild: { branch: "feat/clement-port", sha: "e60" },
          installed: true,
          heldStart: 3,
          heldMemoryPercent: 95,
          heldTasks: ["W1"],
          installing: {
            sha: "abc",
            step: 5,
            doing: "install: go build",
            auto: true,
            error: "go tool task install exited 1",
            log: ["# github.com/empowill/djinn", "undefined: x"],
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
  // One build per branch, each compared with the running Djinn; the newer Djinn ready, named the same way.
  assert.deepEqual(state.builds[0], {
    wishTitle: "Run Djinn on itself",
    project: "djinn",
    branch: "feat/x",
    sha: "abc",
    tasks: ["W5"],
    changes: ["Work of W5"],
    summaries: [
      { code: "W5", title: "Work of W5", summary: "check the banner" },
    ],
    base: "0123",
    running: true,
    continues: "",
    changesCount: 1,
    lacks: ["Work of W4"],
    lacksCount: 1,
    auto: true,
  });
  // The newer Djinn is at the path already; the build's install failed by itself, saying why and its last lines.
  assert.equal(state.installed, true);
  assert.deepEqual(state.installing, {
    sha: "abc",
    step: 5,
    waiting: "",
    doing: "install: go build",
    auto: true,
    error: "go tool task install exited 1",
    log: ["# github.com/empowill/djinn", "undefined: x"],
  });
  assert.deepEqual(
    state.builds.map((b) => b.branch),
    ["feat/x", "feat/clement-port"],
  );
  assert.equal(state.readyBuild.sha, "e60");
  assert.equal(state.readyBuild.running, false);
  // The start resumed no worker, the memory near full: why, the reading, and the task it held.
  assert.deepEqual(state.held, { why: 3, memoryPercent: 95, tasks: ["W1"] });
  assert.deepEqual(asked, []);
  assert.deepEqual(await djinnUpdate.update("abc"), {
    version: "",
    terminals: 0,
    installed: "abc",
  });
  assert.deepEqual(asked, ["abc"]);
});

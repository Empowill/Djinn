// The update client against an in-memory UiService. Run with `node --test shim/`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "esbuild";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-update-"));
test.after(() => fs.rmSync(out, { recursive: true, force: true }));

fs.writeFileSync(
  path.join(out, "entry.ts"),
  `export { createUpdate } from ${JSON.stringify(path.join(here, "update.ts"))};
export { createRouterTransport } from "@connectrpc/connect";
export { UiService } from ${JSON.stringify(path.join(here, "../gen/ts/ui/v1/ui_pb.ts"))};`,
);
await build({
  entryPoints: [path.join(out, "entry.ts")],
  outfile: path.join(out, "update.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  nodePaths: [path.join(here, "../node_modules")],
  logLevel: "error",
});
const { createUpdate, createRouterTransport, UiService } = await import(
  pathToFileURL(path.join(out, "update.mjs")).href
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
        yield { current: "v1", ready: "v2", notResumed: [] };
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
  assert.deepEqual(await djinnUpdate.update(), { version: "v2", terminals: 1 });
  assert.equal(updates, 1);
});

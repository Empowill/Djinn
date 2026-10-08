// The focus client against an in-memory UiService and WishService. Run with `node --test shim/`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "esbuild";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-focus-"));
test.after(() => fs.rmSync(out, { recursive: true, force: true }));

fs.writeFileSync(
  path.join(out, "entry.ts"),
  `export { createFocus } from ${JSON.stringify(path.join(here, "focus.ts"))};
export { createRouterTransport } from "@connectrpc/connect";
export { UiService } from ${JSON.stringify(path.join(here, "../gen/ts/ui/v1/ui_pb.ts"))};
export { WishService } from ${JSON.stringify(path.join(here, "../gen/ts/plan/v1/plan_pb.ts"))};`,
);
await build({
  entryPoints: [path.join(out, "entry.ts")],
  outfile: path.join(out, "focus.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  nodePaths: [path.join(here, "../node_modules")],
  logLevel: "error",
});
const { createFocus, createRouterTransport, UiService, WishService } =
  await import(pathToFileURL(path.join(out, "focus.mjs")).href);

const wishId = "01a11833-a440-7479-a067-52615c91da71";

test("a request to show a wish and its lead reaches every subscriber", async () => {
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        yield { wishId, terminal: `lead-${wishId}` };
        await new Promise(() => {}); // The stream stays open, as the server's does.
      },
    });
    service(WishService, {
      snapshot: () => ({
        export: {
          wish: { id: wishId, title: "Polish the lamp", projectIds: [] },
        },
        projects: [],
      }),
    });
  });
  const focus = createFocus(transport, 10);
  const first = await new Promise((resolve) => focus.subscribe(resolve));
  assert.equal(first.wishId, wishId);
  assert.equal(first.title, "Polish the lamp");
  assert.equal(first.terminal, `lead-${wishId}`);
  assert.equal(first.session.task.title, "Polish the lamp");
  // A part of the page that subscribes later gets it too.
  const late = await new Promise((resolve) => focus.subscribe(resolve));
  assert.equal(late, first);
});

test("a terminal alone shows without reading a wish", async () => {
  let snapshots = 0;
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        yield { wishId: "", terminal: "main" };
        await new Promise(() => {});
      },
    });
    service(WishService, {
      snapshot: () => {
        snapshots++;
        return {};
      },
    });
  });
  const got = await new Promise((resolve) =>
    createFocus(transport, 10).subscribe(resolve),
  );
  assert.deepEqual(
    { wishId: got.wishId, terminal: got.terminal, snapshots },
    { wishId: "", terminal: "main", snapshots: 0 },
  );
});

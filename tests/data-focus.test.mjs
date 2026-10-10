// The focus client against an in-memory UiService. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { createFocus, createRouterTransport, UiService } = await bundle(
  "focus",
  `export { createFocus } from "@/src/data/focus.ts";
export { createRouterTransport } from "@connectrpc/connect";
export { UiService } from "@/gen/ts/ui/v1/ui_pb.ts";`,
);

const wishId = "01a11833-a440-7479-a067-52615c91da71";

test("a request to show a wish and its lead reaches every subscriber", async () => {
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        yield { wishId: "", terminal: "" }; // Nothing to show: skipped.
        yield { wishId, terminal: `lead-${wishId}` };
        await new Promise(() => {}); // The stream stays open, as the server's does.
      },
    });
  });
  const focus = createFocus(transport, 10);
  const first = await new Promise((resolve) => focus.subscribe(resolve));
  assert.deepEqual(first, {
    wishId,
    terminal: `lead-${wishId}`,
    tilasmId: "",
    unknownLink: "",
  });
  // A part of the page that subscribes later gets it too.
  const late = await new Promise((resolve) => focus.subscribe(resolve));
  assert.equal(late, first);
});

test("a broken stream is watched again", async () => {
  let calls = 0;
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        calls++;
        if (calls === 1) throw new Error("djinn restarted");
        yield { wishId: "", terminal: "main" };
        await new Promise(() => {});
      },
    });
  });
  const got = await new Promise((resolve) =>
    createFocus(transport, 10).subscribe(resolve),
  );
  assert.deepEqual(got, {
    wishId: "",
    terminal: "main",
    tilasmId: "",
    unknownLink: "",
  });
  assert.equal(calls, 2);
});

test("a tilasm a link names, a link Djinn does not know, and a link clicked in the page reach every subscriber", async () => {
  const tilasmId = "01a1223a-ae45-728f-8c37-c005eee91edb";
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        yield { wishId, tilasmId };
        yield { unknownLink: "djinn://moon/x" }; // No wish, still shown: the window says so.
        await new Promise(() => {});
      },
    });
  });
  const focus = createFocus(transport, 10);
  const got = [];
  const two = new Promise((resolve) =>
    focus.subscribe((f) => got.push(f) === 2 && resolve()),
  );
  await two;
  assert.deepEqual(got, [
    { wishId, terminal: "", tilasmId, unknownLink: "" },
    { wishId: "", terminal: "", tilasmId: "", unknownLink: "djinn://moon/x" },
  ]);
  // A djinn:// link clicked in the page goes to the subscribers at once, without djinn.
  focus.show({ wishId, tilasmId });
  assert.deepEqual(got[2], { wishId, terminal: "", tilasmId, unknownLink: "" });
});

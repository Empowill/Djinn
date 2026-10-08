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

test("a request to show a wish, its lead and a question reaches every subscriber", async () => {
  const transport = createRouterTransport(({ service }) => {
    service(UiService, {
      async *watchShow() {
        yield { wishId: "", terminal: "" }; // Nothing to show: skipped.
        yield { wishId, terminal: `lead-${wishId}`, target: "question-q1" };
        await new Promise(() => {}); // The stream stays open, as the server's does.
      },
    });
  });
  const focus = createFocus(transport, 10);
  const first = await new Promise((resolve) => focus.subscribe(resolve));
  assert.deepEqual(first, {
    wishId,
    terminal: `lead-${wishId}`,
    target: "question-q1",
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
  assert.deepEqual(got, { wishId: "", terminal: "main", target: "" });
  assert.equal(calls, 2);
});

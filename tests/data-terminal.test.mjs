// The terminal client against an in-memory TerminalService. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const { createTerminal, createRouterTransport, TerminalService } = await bundle(
  "terminal",
  `export { createTerminal } from "@/src/data/terminal.ts";
export { createRouterTransport } from "@connectrpc/connect";
export * from "@/gen/ts/terminal/v1/terminal_pb.ts";`,
);

// server answers each write after a delay, and records the writes in flight and what they carried.
function server(delay) {
  const seen = { writes: [], inFlight: 0, maxInFlight: 0 };
  const transport = createRouterTransport(({ service }) =>
    service(TerminalService, {
      write: async (req) => {
        seen.inFlight++;
        seen.maxInFlight = Math.max(seen.maxInFlight, seen.inFlight);
        await new Promise((r) => setTimeout(r, delay));
        seen.writes.push(new TextDecoder().decode(req.data));
        seen.inFlight--;
        return {};
      },
      list: async () => ({
        terminals: [{ name: "lead-w2" }, { name: "main" }],
      }),
      read: async function* (req) {
        yield { offset: req.fromOffset, data: new TextEncoder().encode("ab") };
        yield {
          offset: req.fromOffset + 2n,
          data: new TextEncoder().encode("c"),
        };
        yield { offset: req.fromOffset + 3n, exited: true, exitCode: 4 };
      },
    }),
  );
  return { api: createTerminal(transport), seen };
}

test("keys leave one request at a time, in the order typed", async () => {
  const { api, seen } = server(5);
  const keys = "hello world".split("");
  await Promise.all(keys.map((k) => api.write("t", k)));
  assert.equal(seen.maxInFlight, 1);
  assert.equal(seen.writes.join(""), "hello world");
  // A few keys keep a request each, as a terminal delivers them.
  assert.equal(seen.writes.length, keys.length);
});

test("a backlog catches up in one request, still in order", async () => {
  const { api, seen } = server(20);
  const keys = Array.from({ length: 40 }, (_, i) =>
    String.fromCharCode(65 + (i % 26)),
  );
  await Promise.all(keys.map((k) => api.write("t", k)));
  assert.equal(seen.writes.join(""), keys.join(""));
  assert.ok(seen.writes.length < keys.length, `${seen.writes.length} requests`);
});

test("read hands each piece with its offset, then the end", async () => {
  const { api } = server(0);
  const pieces = [];
  const end = await api.read(
    "t",
    10n,
    (offset, data) => pieces.push([offset, new TextDecoder().decode(data)]),
    new AbortController().signal,
  );
  assert.deepEqual(pieces, [
    [10n, "ab"],
    [12n, "c"],
  ]);
  assert.deepEqual(end, { exited: true, exitCode: 4 });
});

test("running gives the names of the terminals that run", async () => {
  const { api } = server(0);
  assert.deepEqual(await api.running(), ["lead-w2", "main"]);
});

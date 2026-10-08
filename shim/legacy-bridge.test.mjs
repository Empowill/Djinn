// The import and export of a wish through the shim, against an in-memory WishService. The mission it hands the
// interface must pass the interface's own validation. Run with `node --test shim/`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "esbuild";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-bridge-"));
test.after(() => fs.rmSync(out, { recursive: true, force: true }));

fs.writeFileSync(
  path.join(out, "entry.ts"),
  `export { createDjinn } from ${JSON.stringify(path.join(here, "djinn.ts"))};
export { validateState } from ${JSON.stringify(path.join(here, "../src/session-validation.ts"))};
export { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
export { create, toBinary } from "@bufbuild/protobuf";
export { timestampFromDate } from "@bufbuild/protobuf/wkt";
export * from ${JSON.stringify(path.join(here, "../gen/ts/plan/v1/plan_pb.ts"))};`,
);
await build({
  entryPoints: [path.join(out, "entry.ts")],
  outfile: path.join(out, "bridge.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  nodePaths: [path.join(here, "../node_modules")],
  define: { "import.meta.env.PROD": "false" },
  logLevel: "error",
});
const m = await import(pathToFileURL(path.join(out, "bridge.mjs")).href);
const { create, toBinary, timestampFromDate: ts } = m;

const wishId = "01a11833-a440-7479-a067-52615c91da70";
const projectId = "01a118d8-0390-746b-b68e-c502998114b9";
const at = ts(new Date("2026-10-07T21:10:00Z"));
const T01 = "01a11876-6480-7ec2-9833-abd058ae4a59";
const W1 = "01a11876-6480-7ec2-9833-abd058ae4a5a";

const exported = create(m.WishExportSchema, {
  version: 1,
  createTime: at,
  wish: {
    id: wishId,
    title: "Djinn on Wails",
    projectIds: [projectId],
    createTime: at,
  },
  projects: [
    {
      id: projectId,
      name: "djinn",
      remote: "https://example.com/djinn",
      git: true,
    },
  ],
  tasks: [
    {
      id: T01,
      wishId,
      projectId,
      code: "T01",
      title: "The native window",
      status: m.TaskStatus.DONE,
      createTime: at,
    },
    {
      id: W1,
      wishId,
      projectId,
      code: "W1",
      title: "The browser mode",
      status: m.TaskStatus.INTERRUPTED,
      error: "exported while its worker ran on another machine",
      createTime: at,
      startTime: at,
    },
  ],
  questions: [
    {
      id: "01a11876-6480-7ec2-9833-000000000001",
      code: "Q27",
      wishId,
      text: "Typed core or free blocks?",
      options: ["Typed core — where Djinn computes", "Everything free"],
      context: "It shapes the export.",
      recommendation: "A.",
      createTime: at,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000002",
      code: "Q12",
      wishId,
      text: "Push before it is ready?",
      createTime: at,
      answer: {
        choice: m.Choice.YES,
        note: "No: one commit when ready.",
        createTime: at,
      },
    },
  ],
  blocks: [
    {
      id: "01a11876-6480-7ec2-9833-000000000010",
      wishId,
      kind: "brief",
      title: "The wish",
      content: "On part sur Wails.",
      position: 1000n,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000011",
      wishId,
      kind: "section",
      title: "Lexicon",
      content: "# Lexicon\n\n- wish",
      position: 2000n,
      createTime: at,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000012",
      wishId,
      kind: "log",
      title: "23:10 · Go",
      content: "🟢 Go.",
      position: 3000n,
      createTime: at,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000013",
      wishId,
      kind: "decision",
      title: "No CGO",
      content: "Pure Go.",
      position: 4000n,
      createTime: at,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000014",
      wishId,
      kind: "chart",
      title: "Unknown kind",
      mediaType: "application/json",
      content: '{"a":1}',
      position: 5000n,
    },
    {
      id: "01a11876-6480-7ec2-9833-000000000015",
      wishId,
      taskId: W1,
      kind: "report",
      title: "W1 report",
      content: "Assembled.",
      position: 6000n,
    },
  ],
});
const data = toBinary(m.WishExportSchema, exported);

function server({ already = false } = {}) {
  const calls = { imported: [], exported: [] };
  const transport = m.createRouterTransport(({ service }) =>
    service(m.WishService, {
      importData: (req) => {
        calls.imported.push(req.data);
        if (already)
          throw new m.ConnectError(
            "wish is already here",
            m.Code.AlreadyExists,
          );
        return { wish: exported.wish, projects: [] };
      },
      snapshot: (req) => {
        assert.equal(req.wishId, wishId);
        return {
          export: exported,
          projects: [
            {
              id: projectId,
              name: "djinn",
              directory: "",
              remote: "https://example.com/djinn",
              git: true,
              createTime: at,
            },
          ],
        };
      },
      list: () => ({ wishes: [exported.wish] }),
      export: (req) => {
        calls.exported.push(req.wishId);
        return { file: "/home/me/Downloads/djinn-on-wails.djinn", size: 10n };
      },
    }),
  );
  return { transport, calls };
}

const file = (bytes) => ({
  size: bytes.length,
  arrayBuffer: async () =>
    bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.length),
});

test("an imported wish becomes a mission the interface accepts", async () => {
  const { transport, calls } = server();
  const djinn = m.createDjinn(transport, async () => file(data));
  const session = await djinn.importSession();
  assert.equal(calls.imported.length, 1);
  assert.equal(session.format, "djinn-session");
  // What the interface does with it: use-djinn.ts validates the session as a saved state.
  const state = m.validateState({
    version: session.version,
    tasks: [session.task],
    projects: session.projects,
  });
  const mission = state.tasks[0];
  assert.equal(mission.title, "Djinn on Wails");
  assert.equal(mission.brief, "On part sur Wails.");
  const q27 = mission.questions.find((q) => q.id === "Q27");
  assert.deepEqual(
    q27.options.map((o) => [o.id, o.label]),
    [
      ["A", "Typed core"],
      ["B", "Everything free"],
    ],
  );
  assert.equal(q27.recommendation, "A.");
  assert.match(
    mission.questions.find((q) => q.id === "Q12").answer,
    /one commit/,
  );
  assert.deepEqual(
    mission.artifacts.map((a) => a.title),
    ["Lexicon", "Unknown kind"],
  );
  assert.match(mission.artifacts[1].content, /^```json\n/);
  assert.deepEqual(mission.events.map((e) => e.type).sort(), [
    "decision",
    "note",
  ]);
  assert.deepEqual(
    mission.agents.map((a) => [a.id, a.status, a.summary]),
    [["W1", "blocked", "**W1 report**\n\nAssembled."]],
  );
  assert.deepEqual(
    mission.workItems.map((w) => [w.title, w.status]),
    [["T01 · The native window", "done"]],
  );
  assert.equal(state.projects[0].name, "djinn");
});

test("a wish already here is shown, not imported twice", async () => {
  const { transport } = server({ already: true });
  const djinn = m.createDjinn(transport, async () => file(data));
  const session = await djinn.importSession();
  assert.equal(session.task.id, wishId);
});

test("a cancelled pick imports nothing", async () => {
  const { transport, calls } = server();
  const djinn = m.createDjinn(transport, async () => null);
  assert.equal(await djinn.importSession(), null);
  assert.equal(calls.imported.length, 0);
});

test("a mission that is a wish exports through the server; another one says why not", async () => {
  const { transport, calls } = server();
  const djinn = m.createDjinn(transport);
  const result = await djinn.exportSession({
    task: { title: "djinn on wails" },
  });
  assert.deepEqual(calls.exported, [wishId]);
  assert.equal(result.filename, "djinn-on-wails.djinn");
  await assert.rejects(
    djinn.exportSession({ task: { title: "Something else" } }),
    (error) => error.code === "not_available",
  );
});

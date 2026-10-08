// The shim against an in-memory UiService. Run with `node --test shim/`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "esbuild";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-shim-"));
test.after(() => fs.rmSync(out, { recursive: true, force: true }));

// Bundle the shim with the test's own Connect code, so both share one copy of the generated descriptors.
fs.writeFileSync(
  path.join(out, "entry.ts"),
  `export { createDjinn, djinnTransport } from ${JSON.stringify(path.join(here, "djinn.ts"))};
export { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
export { create, fromBinary, toBinary } from "@bufbuild/protobuf";
export * from ${JSON.stringify(path.join(here, "../gen/ts/ui/v1/ui_pb.ts"))};`,
);
await build({
  entryPoints: [path.join(out, "entry.ts")],
  outfile: path.join(out, "shim.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  nodePaths: [path.join(here, "../node_modules")],
  define: { "import.meta.env.PROD": "false" },
  logLevel: "error",
});
const shim = await import(pathToFileURL(path.join(out, "shim.mjs")).href);
const { createDjinn, createRouterTransport, ConnectError, Code, UiService } =
  shim;

function server() {
  const calls = { saved: [], opened: [] };
  let stored = "";
  const transport = createRouterTransport(({ service }) =>
    service(UiService, {
      getEnvironment: () => ({
        version: "1.2.3",
        platform: "linux",
        providers: [
          {
            id: "codex",
            name: "Codex",
            available: true,
            command: "/usr/bin/codex",
          },
        ],
      }),
      loadState: () => ({ stateJson: stored }),
      saveState: (req) => {
        calls.saved.push(req.stateJson);
        stored = req.stateJson;
        return {};
      },
      validateProject: (req) => {
        if (!req.directory.startsWith("/"))
          throw new ConnectError(
            "project directory: the path must be absolute",
            Code.InvalidArgument,
          );
        return { directory: `/real${req.directory}`, git: true };
      },
      openExternal: (req) => {
        if (!req.url.startsWith("http"))
          throw new ConnectError(
            "only a short http or https link without credentials may be opened",
            Code.InvalidArgument,
          );
        calls.opened.push(req.url);
        return { url: req.url };
      },
      notifyQuestion: () => ({ shown: false }),
    }),
  );
  return { djinn: createDjinn(transport), calls };
}

test("covers every method of the Electron preload", () => {
  const preload = fs.readFileSync(
    path.join(here, "../electron/preload.cjs"),
    "utf8",
  );
  const methods = [...preload.matchAll(/^ {2}(\w+): \(/gm)]
    .map((m) => m[1])
    .filter((m) => m !== "onEvent");
  assert.equal(methods.length, 25);
  const { djinn } = server();
  // Read by the startup with a fallback when absent; a rejection would suspend autosave.
  const absent = [
    "getRuntimeSnapshot",
    "getPendingPermissions",
    "getMissionInteractions",
  ];
  for (const name of methods)
    assert.equal(
      typeof djinn[name],
      absent.includes(name) ? "undefined" : "function",
      name,
    );
  assert.equal(typeof djinn.onEvent, "function");
  assert.ok(Object.isFrozen(djinn));
});

test("getEnvironment has the shape of the preload", async () => {
  const { djinn } = server();
  assert.deepEqual(await djinn.getEnvironment(), {
    platform: "linux",
    appVersion: "1.2.3",
    providers: [
      {
        id: "codex",
        name: "Codex",
        available: true,
        authenticated: null,
        command: "/usr/bin/codex",
      },
    ],
  });
});

test("the state is null until saved, then comes back as saved", async () => {
  const { djinn, calls } = server();
  assert.equal(await djinn.loadState(), null);
  const state = { version: 2, tasks: [{ id: "a", title: "été" }] };
  assert.deepEqual(await djinn.saveState(state), { saved: true });
  assert.deepEqual(calls.saved, [JSON.stringify(state)]);
  assert.deepEqual(await djinn.loadState(), state);
});

test("validateProject keeps the project and canonicalizes its directory", async () => {
  const { djinn } = server();
  const project = { id: "p", name: "P", directory: "/repo", locations: {} };
  assert.deepEqual(await djinn.validateProject(project), {
    ...project,
    directory: "/real/repo",
  });
  await assert.rejects(
    djinn.validateProject({ ...project, directory: "repo" }),
    {
      code: "invalid_argument",
      message: "project directory: the path must be absolute",
    },
  );
});

test("openExternal and notifyQuestion", async () => {
  const { djinn, calls } = server();
  assert.deepEqual(await djinn.openExternal("https://example.com/"), {
    opened: true,
    url: "https://example.com/",
  });
  await assert.rejects(djinn.openExternal("file:///etc/passwd"), {
    code: "invalid_argument",
  });
  assert.deepEqual(calls.opened, ["https://example.com/"]);
  assert.deepEqual(
    await djinn.notifyQuestion({
      taskId: "t",
      questionId: "q",
      title: "T",
      body: "B",
    }),
    {
      shown: false,
      reason: "unsupported",
    },
  );
});

test("the other methods reject with a clear error", async () => {
  const { djinn } = server();
  await assert.rejects(djinn.selectDirectory(), {
    code: "not_available",
    message: "selectDirectory is not available yet",
  });
  await assert.rejects(djinn.startRun({ taskId: "t" }), {
    code: "not_available",
  });
});

test("onEvent returns an unsubscribe function", () => {
  const { djinn } = server();
  assert.equal(typeof djinn.onEvent(() => undefined), "function");
  assert.equal(typeof djinn.onEvent("not a function"), "function");
});

test("the window speaks binary Protobuf, uncompressed", async () => {
  const requests = [];
  // A fetch that answers UiService.SaveState as the Go server does.
  const fetch = async (url, init) => {
    const headers = new Headers(init.headers);
    const body = new Uint8Array(await new Response(init.body).arrayBuffer());
    requests.push({ url: String(url), headers, body });
    const res = shim.toBinary(
      shim.UiServiceSaveStateResponseSchema,
      shim.create(shim.UiServiceSaveStateResponseSchema),
    );
    return new Response(res, {
      status: 200,
      headers: { "Content-Type": "application/proto" },
    });
  };
  const djinn = createDjinn(shim.djinnTransport("http://djinn.test", fetch));
  const state = { version: 2, title: 'un "flux" été' };
  assert.deepEqual(await djinn.saveState(state), { saved: true });
  assert.equal(requests.length, 1);
  const [req] = requests;
  assert.equal(req.url, "http://djinn.test/ui.v1.UiService/SaveState");
  assert.equal(req.headers.get("content-type"), "application/proto");
  // connect-web asks for no compression: the browser alone adds Accept-Encoding, which the server ignores.
  assert.equal(req.headers.get("accept-encoding"), null);
  const sent = shim.fromBinary(shim.UiServiceSaveStateRequestSchema, req.body);
  assert.equal(sent.stateJson, JSON.stringify(state));
});

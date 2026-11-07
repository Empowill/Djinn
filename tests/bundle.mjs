// bundle builds a TypeScript entry with esbuild and imports it, for the Node tests of src/data/: the entry and the
// test's Connect code are bundled together, so both share one copy of the generated descriptors.
import { createHash } from "node:crypto";
import fs from "node:fs";
import module from "node:module";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "esbuild";

export const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

// raw loads a "?raw" import as Vite does: the file's text.
const raw = {
  name: "raw",
  setup(build) {
    build.onResolve({ filter: /\?raw$/ }, (args) => ({
      path: path.join(args.resolveDir, args.path.replace(/\?raw$/, "")),
      namespace: "raw",
    }));
    build.onLoad({ filter: /.*/, namespace: "raw" }, (args) => ({
      contents: fs.readFileSync(args.path, "utf8"),
      loader: "text",
    }));
  },
};

// The bundles are kept under node_modules/.cache, one per name and source, with the size and time of each file they
// read: a test file whose bundle is up to date imports it, and the test files Node runs together wait for the one that
// builds it (the screens' tests share one).
const cache = path.join(root, "node_modules", ".cache", "djinn-tests");

// bundle bundles source (TypeScript, paths relative to the repository), unless its bundle is up to date, and imports it.
export async function bundle(name, source) {
  const key = createHash("sha256").update(source).digest("hex").slice(0, 16);
  const base = path.join(cache, `${name}-${key}`);
  const lock = `${base}.lock`;
  fs.mkdirSync(cache, { recursive: true });
  while (!fresh(`${base}.json`)) {
    try {
      fs.mkdirSync(lock);
    } catch (err) {
      if (err.code !== "EEXIST") throw err;
      // Another test file builds it; a lock older than a minute was left by one that died.
      const since = fs.statSync(lock, { throwIfNoEntry: false })?.mtimeMs;
      if (since && Date.now() - since > 60_000)
        fs.rmSync(lock, { recursive: true, force: true });
      await new Promise((resolve) => setTimeout(resolve, 50));
      continue;
    }
    try {
      if (!fresh(`${base}.json`)) await compile(name, source, base, key);
    } finally {
      fs.rmSync(lock, { recursive: true, force: true });
    }
    // Built: up to date, even when a file it read changed meanwhile and left it unrecorded.
    break;
  }
  // V8 keeps the bundle compiled for the next test file that imports it (Node 22.1 and later).
  module.enableCompileCache?.(path.join(cache, "v8"));
  return await import(pathToFileURL(`${base}.mjs`).href);
}

// fresh says whether every file a bundle read still has the size and time it had.
function fresh(meta) {
  const bundleFile = meta.replace(/\.json$/, ".mjs");
  if (!fs.existsSync(bundleFile)) return false;
  let inputs;
  try {
    inputs = JSON.parse(fs.readFileSync(meta, "utf8"));
  } catch {
    return false;
  }
  if (!inputs || typeof inputs !== "object") return false;
  return Object.entries(inputs).every(([file, [size, time]]) => {
    const now = fs.statSync(file, { throwIfNoEntry: false });
    return now?.size === size && now.mtimeMs === time;
  });
}

// compile bundles source into base.mjs, then writes base.json, the files it read; the bundles of the same name it
// replaces are removed.
async function compile(name, source, base, key) {
  fs.writeFileSync(
    `${base}.ts`,
    source.replace(/"@\/([^"]+)"/g, (_, p) =>
      JSON.stringify(path.join(root, p)),
    ),
  );
  const started = Date.now();
  const tmpMjs = `${base}.${process.pid}.tmp.mjs`;
  let metafile;
  try {
    const res = await build({
      entryPoints: [`${base}.ts`],
      outfile: tmpMjs,
      absWorkingDir: root,
      metafile: true,
      bundle: true,
      format: "esm",
      platform: "node",
      nodePaths: [path.join(root, "node_modules")],
      // React's development build, which Node picks with no NODE_ENV, alone: its production build stays out.
      define: {
        "import.meta.env.PROD": "false",
        "process.env.NODE_ENV": '"development"',
      },
      // The ES modules of a package before its CommonJS: the icons a component does not draw stay out.
      mainFields: ["module", "main"],
      // An imported image is a URL, as Vite makes it (the brand mark, src/frame.tsx).
      loader: { ".json": "json", ".css": "empty", ".svg": "dataurl" },
      // CommonJS dependencies (react-dom/server) require Node's own modules.
      banner: {
        js: 'import { createRequire } from "node:module"; const require = createRequire(import.meta.url);',
      },
      plugins: [raw],
      logLevel: "error",
    });
    metafile = res.metafile;
    fs.renameSync(tmpMjs, `${base}.mjs`);
  } finally {
    fs.rmSync(tmpMjs, { force: true });
  }
  const inputs = {};
  for (const input of Object.keys(metafile.inputs)) {
    const file = path.resolve(root, input.replace(/^raw:/, ""));
    const now = fs.statSync(file);
    // A file changed while it was read: no record, the next test file builds again.
    if (Math.floor(now.mtimeMs) > started && file !== `${base}.ts`) return;
    inputs[file] = [now.size, now.mtimeMs];
  }
  const tmpJson = `${base}.${process.pid}.tmp.json`;
  try {
    fs.writeFileSync(tmpJson, JSON.stringify(inputs));
    fs.renameSync(tmpJson, `${base}.json`);
  } finally {
    fs.rmSync(tmpJson, { force: true });
  }
  for (const file of fs.readdirSync(cache)) {
    const match = file.match(
      new RegExp(`^${name}-([0-9a-f]{16})\\.(mjs|json|ts)$`),
    );
    if (match && match[1] !== key)
      fs.rmSync(path.join(cache, file), { force: true });
  }
}

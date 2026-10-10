// bundle builds a TypeScript entry with esbuild and imports it, for the Node tests of src/data/: the entry and the
// test's Connect code are bundled together, so both share one copy of the generated descriptors.
import fs from "node:fs";
import os from "node:os";
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

// bundle writes source (TypeScript, paths relative to the repository) to a temporary entry, bundles and imports it.
export async function bundle(name, source) {
  const out = fs.mkdtempSync(path.join(os.tmpdir(), `djinn-${name}-`));
  try {
    fs.writeFileSync(
      path.join(out, "entry.ts"),
      source.replace(/"@\/([^"]+)"/g, (_, p) =>
        JSON.stringify(path.join(root, p)),
      ),
    );
    await build({
      entryPoints: [path.join(out, "entry.ts")],
      outfile: path.join(out, `${name}.mjs`),
      bundle: true,
      format: "esm",
      platform: "node",
      nodePaths: [path.join(root, "node_modules")],
      define: { "import.meta.env.PROD": "false" },
      // An imported image is a URL, as Vite makes it (the brand mark, src/frame.tsx).
      loader: { ".json": "json", ".css": "empty", ".svg": "dataurl" },
      // CommonJS dependencies (react-dom/server) require Node's own modules.
      banner: {
        js: 'import { createRequire } from "node:module"; const require = createRequire(import.meta.url);',
      },
      plugins: [raw],
      logLevel: "error",
    });
    return await import(pathToFileURL(path.join(out, `${name}.mjs`)).href);
  } finally {
    fs.rmSync(out, { recursive: true, force: true });
  }
}

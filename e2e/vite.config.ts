// Builds the stream check page into dist/e2e/, next to the interface, for `task e2e` only: the interface's own
// build (`npm run build`) empties dist/ and never contains it.
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const root = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  root,
  base: "./",
  build: {
    outDir: "../dist/e2e",
    emptyOutDir: true,
    rollupOptions: { input: { stream: `${root}stream.html` } },
  },
});

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
import { rendererPolicy } from "./src/renderer-policy";
import { MERMAID_FRAME, mermaidFrame } from "./src/mermaid-frame";
// The document Mermaid draws in: emitted by the build, served by the development server.
const diagramFrame = () =>
  mermaidFrame(readFileSync("src/vendor/mermaid-11.16.1.min.js", "utf8"));
// `go tool task dev` (tools/devloop) sets DJINN_DEV_API: the page's Connect calls go through its relay to the djinn
// in development, which it restarts when Go changes.
const devApi = process.env.DJINN_DEV_API;
export default defineConfig({
  plugins: [
    react(),
    {
      name: "renderer-csp",
      transformIndexHtml(_html, context) {
        const port = context.server?.config.server.port || 4317;
        return [
          {
            tag: "meta",
            attrs: {
              "http-equiv": "Content-Security-Policy",
              content: rendererPolicy(
                context.server ? `http://127.0.0.1:${port}` : undefined,
              ),
            },
            injectTo: "head",
          },
        ];
      },
    },
    {
      name: "mermaid-frame",
      configureServer(server) {
        server.middlewares.use(`/${MERMAID_FRAME}`, (_req, res) => {
          void diagramFrame().then((html) => {
            res.setHeader("Content-Type", "text/html; charset=utf-8");
            res.end(html);
          });
        });
      },
      async generateBundle() {
        this.emitFile({
          type: "asset",
          fileName: MERMAID_FRAME,
          source: await diagramFrame(),
        });
      },
    },
  ],
  base: "./",
  server: {
    host: "127.0.0.1",
    port: 4317,
    strictPort: true,
    proxy: devApi
      ? {
          "^/[a-z]+\\.v[0-9]+\\.[A-Za-z]+Service/": { target: devApi },
          // The documentation site, which djinn serves.
          "^/docs/": { target: devApi },
        }
      : undefined,
  },
});

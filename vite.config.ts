import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { rendererPolicy } from "./src/renderer-policy";
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
  ],
  base: "./",
  server: {
    host: "127.0.0.1",
    port: 4317,
    strictPort: true,
    proxy: devApi
      ? { "^/[a-z]+\\.v[0-9]+\\.[A-Za-z]+Service/": { target: devApi } }
      : undefined,
  },
});

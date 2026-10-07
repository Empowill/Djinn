import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { rendererPolicy } from "./src/renderer-policy";
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
  server: { host: "127.0.0.1", port: 4317, strictPort: true },
});

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
    {
      // Keep the initial document free of the 2 MB JS bundle and 200 kB stylesheet so WebKitGTK finishes the initial
      // document load immediately, lifts its visual-update suppression, and paints #djinn-splash on the first frame
      // (< 200 ms) before boot.js attaches the bundle stylesheet and module script.
      name: "splash-first",
      apply: "build",
      generateBundle() {
        this.emitFile({
          type: "asset",
          fileName: "assets/boot.js",
          source: [
            "let booted = false;",
            "const boot = () => {",
            "  if (booted) return;",
            "  booted = true;",
            '  const css = document.querySelector(\'meta[name="djinn-entry-css"]\')?.getAttribute("content");',
            "  if (css) {",
            '    const link = document.createElement("link");',
            '    link.rel = "stylesheet";',
            '    link.crossOrigin = "";',
            "    link.href = css;",
            "    document.head.appendChild(link);",
            "  }",
            '  const js = document.querySelector(\'meta[name="djinn-entry-js"]\')?.getAttribute("content");',
            "  if (js) {",
            '    const script = document.createElement("script");',
            '    script.type = "module";',
            '    script.crossOrigin = "";',
            "    script.src = js;",
            "    document.head.appendChild(script);",
            "  }",
            "};",
            "const schedule = () => {",
            "  requestAnimationFrame(() => setTimeout(boot, 0));",
            "  setTimeout(boot, 32);",
            "};",
            'if (document.readyState === "complete") schedule();',
            'else window.addEventListener("load", schedule, { once: true });',
            "",
          ].join("\n"),
        });
      },
      transformIndexHtml: {
        order: "post",
        handler(html) {
          return html
            .replace(
              /<script type="module" crossorigin src="([^"]+)"><\/script>/g,
              '<meta name="djinn-entry-js" content="$1"><script type="module" src="./assets/boot.js"></script>',
            )
            .replace(
              /<link rel="stylesheet" crossorigin href="([^"]+)">/g,
              '<meta name="djinn-entry-css" content="$1">',
            );
        },
      },
    },
  ],
  base: "./",
  // ES2022, as tsconfig.json: every webview of Wails runs it (WebKitGTK, WKWebView, WebView2). Vite's default, ES2020,
  // lowers `x ||= {}`, and esbuild 0.25 drops the variable when it minifies that: xterm.js's answer to DECRQM threw
  // "n is not defined" and left the terminal blank, the first frame of agy (Antigravity) included.
  build: { target: "es2022" },
  server: {
    host: "127.0.0.1",
    port: 4317,
    strictPort: true,
    proxy: devApi
      ? {
          "^/[a-z]+\\.v[0-9]+\\.[A-Za-z]+Service/": { target: devApi },
          // The documentation site, which djinn serves.
          "^/docs/": { target: devApi },
          "^/tilasm/": { target: devApi },
        }
      : undefined,
  },
});

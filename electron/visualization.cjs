"use strict";
const crypto = require("node:crypto");
const theme = require("./visualization-theme.json");
const policy =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";
function visualizationHtml(source, token) {
  if (
    typeof source !== "string" ||
    source.length > 500000 ||
    source.includes("\0")
  )
    throw new Error(
      "Visualization source is invalid or exceeds 500000 characters",
    );
  // Place policy before any supplied HTML. The unique, sandboxed document has
  // no Electron bridge and can only report bounded size to its parent.
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${policy}"><style>${theme.css}</style></head><body>${source}<script>const report=()=>parent.postMessage({type:'djinn:visualization-height',token:${JSON.stringify(token)},height:Math.min(4000,Math.max(120,document.documentElement.scrollHeight))},'*');new ResizeObserver(report).observe(document.body);report();</script></body></html>`;
}
class VisualizationRegistry {
  constructor() {
    this.documents = new Map();
  }
  create(source) {
    const token = crypto.randomUUID();
    const html = visualizationHtml(source, token);
    this.documents.set(token, html);
    while (this.documents.size > 50)
      this.documents.delete(this.documents.keys().next().value);
    return { url: `djinn-visualization://${token}/`, token };
  }
  get(url) {
    try {
      const parsed = new URL(url);
      if (
        parsed.protocol !== "djinn-visualization:" ||
        parsed.username ||
        parsed.password ||
        parsed.port ||
        parsed.pathname !== "/" ||
        parsed.search
      )
        return undefined;
      return this.documents.get(parsed.hostname);
    } catch {
      return undefined;
    }
  }
}
module.exports = { visualizationHtml, VisualizationRegistry, policy };

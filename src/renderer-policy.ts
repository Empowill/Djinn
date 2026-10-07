// Visualizations may load only as isolated documents, never a renderer page.
// Keep this parent policy active in Vite dev as well as the packaged application.
export function rendererPolicy(devOrigin?: string): string {
  const socket = devOrigin
    ? new URL(devOrigin.replace(/^http/, "ws")).origin
    : "";
  return [
    "default-src 'self'",
    `script-src 'self'${devOrigin ? " 'unsafe-inline'" : ""}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self'",
    `connect-src 'self'${socket ? ` ${socket}` : ""}`,
    "frame-src blob: djinn-visualization:",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'none'",
  ].join("; ");
}

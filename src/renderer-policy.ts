// Frames load only documents of the page's own origin, such as the Mermaid frame (mermaid-frame.ts): a blob: or
// srcdoc frame would inherit this policy, and its inline scripts would never run.
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
    "frame-src 'self'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'none'",
  ].join("; ");
}

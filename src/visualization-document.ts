import { css as themeCss } from "../electron/visualization-theme.json";
import { t } from "./i18n";
// Keep the browser fallback as restrictive as the native document protocol.
export const VISUALIZATION_POLICY =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";

export function visualizationDocument(source: string, token: string) {
  if (source.length > 500000 || source.includes("\0"))
    throw new Error(t("visualization.source_invalid"));
  // UUID only: the token is interpolated into an inline script.
  if (!/^[a-zA-Z0-9-]+$/.test(token))
    throw new Error(t("visualization.token_invalid"));
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${VISUALIZATION_POLICY}"><meta name="referrer" content="no-referrer"><style>${themeCss}</style><script>(()=>{const token=${JSON.stringify(token)};const send=(type,data)=>parent.postMessage({type,token,...data},'*');addEventListener('error',()=>send('djinn:visualization-error',{message:${JSON.stringify(t("visualization.script_error"))}}));addEventListener('unhandledrejection',()=>send('djinn:visualization-error',{message:${JSON.stringify(t("visualization.interaction_failed"))}}));addEventListener('DOMContentLoaded',()=>{const report=()=>send('djinn:visualization-height',{height:Math.min(4000,Math.max(120,Math.ceil(document.documentElement.scrollHeight)))});new ResizeObserver(report).observe(document.body);report();});})();</script></head><body>${source}</body></html>`;
}

export function visualizationMessage(
  event: MessageEvent,
  frameWindow: Window | null,
  tokens: readonly string[],
) {
  const data: unknown = event.data;
  if (
    !frameWindow ||
    event.source !== frameWindow ||
    event.origin !== "null" ||
    !data ||
    typeof data !== "object" ||
    Array.isArray(data)
  )
    return null;
  const message = data as Record<string, unknown>;
  if (typeof message.token !== "string" || !tokens.includes(message.token))
    return null;
  if (
    message.type === "djinn:visualization-height" &&
    typeof message.height === "number" &&
    Number.isFinite(message.height) &&
    message.height >= 120 &&
    message.height <= 4000
  )
    return { height: Math.ceil(message.height) };
  if (
    message.type === "djinn:visualization-error" &&
    typeof message.message === "string" &&
    message.message.length <= 300
  )
    return { error: message.message };
  return null;
}
